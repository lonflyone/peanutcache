// Copyright 2021 Peanutzhen. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package registry

import (
	"context"
	"log"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/naming/resolver"
	"google.golang.org/grpc"
)

// EtcdDial 向grpc请求一个服务
// 通过提供一个etcd client和service name即可获得Connection
func EtcdDial(c *clientv3.Client, service string) (g *grpc.ClientConn, e error) {
	etcdResolver, err := resolver.NewBuilder(c)
	if err != nil {
		return nil, err
	}
	g, e = grpc.Dial(
		"etcd:///"+service,
		grpc.WithResolvers(etcdResolver),
		grpc.WithInsecure(),
		grpc.WithBlock(),
	)
	return
}

func GetPeers(client *clientv3.Client) []string {
	var peers []string
	// 获取 etcd 集群成员列表
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	resp, err := client.Get(ctx, "peanutcache", clientv3.WithPrefix())
	defer cancel()
	if err != nil {
		log.Fatalf("Failed to get etcd members: %v", err)
	}
	for _, kv := range resp.Kvs {
		peers = append(peers, strings.SplitN(string(kv.Key), "/", 3)[2])
	}
	return peers
}
