// Copyright 2021 Peanutzhen. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package peanutcache

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"geecache/peanutcache/consistenthash"
	pb "geecache/peanutcache/peanutcachepb"
	"geecache/peanutcache/registry"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
)

// server 模块为peanutcache之间提供通信能力
// 这样部署在其他机器上的cache可以通过访问server获取缓存
// 至于找哪台主机 那是一致性哈希的工作了

const (
	defaultAddr     = "127.0.0.1:6324"
	defaultReplicas = 50
)

var (
	defaultEtcdConfig = clientv3.Config{
		Endpoints:   []string{"localhost:2379"},
		DialTimeout: 5 * time.Second,
	}
)

// server 和 Group 是解耦合的 所以server要自己实现并发控制
type server struct {
	pb.UnimplementedPeanutCacheServer

	addr       string     // format: ip:port
	status     bool       // true: running false: stop
	stopSignal chan error // 通知registry revoke服务
	mu         sync.Mutex
	consHash   *consistenthash.Consistency
	clients    map[string]*client
}

// NewServer 创建cache的svr 若addr为空 则使用defaultAddr
func NewServer(addr string) (*server, error) {
	if addr == "" {
		addr = defaultAddr
	}
	if !validPeerAddr(addr) {
		return nil, fmt.Errorf("invalid addr %s, it should be x.x.x.x:port", addr)
	}
	return &server{addr: addr}, nil
}

// Get 实现PeanutCache service的Get接口
func (s *server) Get(ctx context.Context, in *pb.GetRequest) (*pb.GetResponse, error) {
	group, key := in.GetGroup(), in.GetKey()
	resp := &pb.GetResponse{}
	fmt.Printf("[peanutcache_svr %s] Recv RPC Request - (%s)/(%s)", s.addr, group, key)
	if key == "" {
		return resp, fmt.Errorf("key required")
	}
	g := GetGroup(group)
	if g == nil {
		return resp, fmt.Errorf("group not found")
	}
	view, err := g.Get(key)
	if err != nil {
		return resp, err
	}
	resp.Value = view.ByteSlice()
	return resp, nil
}

// Start 启动cache服务
func (s *server) Start() error {
	s.mu.Lock()
	if s.status == true {
		s.mu.Unlock()
		return fmt.Errorf("server already started")
	}
	// -----------------启动服务----------------------
	// 1. 设置status为true 表示服务器已在运行
	// 2. 初始化stop channal,这用于通知registry stop keep alive
	// 3. 初始化tcp socket并开始监听
	// 4. 注册rpc服务至grpc 这样grpc收到request可以分发给server处理
	// 5. 将自己的服务名/Host地址注册至etcd 这样client可以通过etcd
	//    获取服务Host地址 从而进行通信。这样的好处是client只需知道服务名
	//    以及etcd的Host即可获取对应服务IP 无需写死至client代码中
	// ----------------------------------------------
	s.status = true
	s.stopSignal = make(chan error)

	port := strings.Split(s.addr, ":")[1]
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterPeanutCacheServer(grpcServer, s)

	// 注册服务至etcd
	go func() {
		// Register never return unless stop singnal received
		err := registry.Register("peanutcache/"+s.addr, s.addr, s.stopSignal)
		if err != nil {
			log.Fatalf(err.Error())
		}
		// Close channel
		close(s.stopSignal)
		// Close tcp listen
		err = lis.Close()
		if err != nil {
			log.Fatalf(err.Error())
		}
		log.Printf("[%s] Revoke service and close tcp socket ok.", s.addr)
	}()

	log.Printf("[%s] register service ok\n", s.addr)
	s.mu.Unlock()

	if err := grpcServer.Serve(lis); s.status && err != nil {
		return fmt.Errorf("failed to serve: %v", err)
	}
	return nil
}

// SetPeers 将各个远端主机IP配置到Server里
// 这样Server就可以Pick他们了
// 注意: 此操作是*覆写*操作！
// 注意: peersIP必须满足 x.x.x.x:port的格式
func (s *server) SetPeers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cli, _ := clientv3.New(defaultEtcdConfig)
	// defer cli.Close()
	// 获取所有远端主机IP
	peersAddr := registry.GetPeers(cli)
	peersAddr = append(peersAddr, s.addr)
	s.consHash = consistenthash.New(defaultReplicas, nil)
	s.consHash.Register(peersAddr...)
	s.clients = make(map[string]*client)
	for _, peerAddr := range peersAddr {
		if !validPeerAddr(peerAddr) {
			panic(fmt.Sprintf("[peer %s] invalid address format, it should be x.x.x.x:port", peerAddr))
		}
		service := fmt.Sprintf("peanutcache/%s", peerAddr)
		s.clients[peerAddr] = NewClient(service)
	}
	// 开一个协程一直循环 直到stopSignal收到信息才退出
	go func() {
		for {
			select {
			case <-s.stopSignal:
				log.Println("stop set peers")
				return
			case <-cli.Ctx().Done():
				fmt.Println("What happend")
				return
			default:
				time.Sleep(2 * time.Second)
				peersAddrTs := registry.GetPeers(cli)
				peersAddrNs := make([]string, 0)

				// 移除所有已下线的peer
				for pAddr, _ := range s.clients {
					if !Contains(peersAddrTs, pAddr) {
						s.consHash.Deregister(pAddr)
						delete(s.clients, pAddr)
					} else {
						peersAddrNs = append(peersAddrNs, pAddr)
					}
				}
				//滚动更新
				for _, peerAddr := range peersAddrTs {
					if !Contains(peersAddrNs, peerAddr) {
						s.consHash.Register(peerAddr)
						service := fmt.Sprintf("peanutcache/%s", peerAddr)
						s.clients[peerAddr] = NewClient(service)
					}
				}
			}
		}
	}()

}
func Contains(list []string, str string) bool {
	for _, s := range list {
		if s == str {
			return true
		}
	}
	return false
}

// Pick 根据一致性哈希选举出key应存放在的cache
// return false 代表从本地获取cache
func (s *server) Pick(key string) (Fetcher, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	peerAddr := s.consHash.GetPeer(key)
	// Pick itself
	if peerAddr == s.addr {
		log.Printf("ooh! pick myself, I am %s\n", s.addr)
		return nil, false
	}
	log.Printf("[cache %s] pick remote peer: %s\n", s.addr, peerAddr)
	return s.clients[peerAddr], true
}

// Stop 停止server运行 如果server没有运行 这将是一个no-op
func (s *server) Stop() {
	s.mu.Lock()
	if s.status == false {
		s.mu.Unlock()
		return
	}
	s.stopSignal <- nil // 发送停止keepalive信号
	s.status = false    // 设置server运行状态为stop
	s.clients = nil     // 清空一致性哈希信息 有助于垃圾回收
	s.consHash = nil
	s.mu.Unlock()
}

// 测试Server是否实现了Picker接口
var _ Picker = (*server)(nil)
