# PeanutCache

![](https://img.shields.io/badge/license-MIT-blue)![](https://img.shields.io/github/stars/peanutzhen/peanutcache?style=plastic)

The [**gRPC**](https://github.com/grpc/grpc-go) implementation of [**groupcache**](https://github.com/golang/groupcache): A high performance, open source, using RPC framework that  communicated with each cache node. Cache service can register to [**etcd**](https://github.com/etcd-io/etcd), and each cache client can dicovery the service list by etcd.For more information see the [groupcache](https://github.com/golang/groupcache), or [**geecache**](https://geektutu.com/post/geecache.html).

## Prerequisites

- **Golang** 1.16 or later
- **Etcd** v3.4.0 or later
- **gRPC-go** v1.38.0 or later
- **protobuf** v1.26.0 or later

## TodoList

欢迎大家Pull Request，可随时联系作者。

1. 将一致性哈希从`Server`抽象出来，作为单独的一个`Proxy`层。避免在每个节点自己做一致性哈希，这样存在哈希环不一致的情况。
2. 增加缓存持久化的能力。
3. 改进`LRU cache`，使其具备`TTL`的能力，以及改进锁的粒度，提高并发度。

## Installation

With [Go module]() support (Go 1.11+), simply add the following import

```go
import "github.com/peanutzhen/peanutcache"
```

to your code, and then `go [build|run|test]` will automatically fetch the necessary dependencies.

Otherwise, to install the `peanutcache` package, run the following command:

```bash
$ go get -u github.com/peanutzhen/peanutcache
```

## Usage

Here, give a example to use it `example.go`:

```go
// example.go file
// 运行前，你需要在本地启动Etcd实例，作为服务中心。

// 模拟MySQL数据库 用于peanutcache从数据源获取值
var mysql = map[string]string{
	"Tom":  "630",
	"Jack": "589",
	"Sam":  "567",
}

func startAPIServer(apiAddr string, gee *peanutcache.Group) {
	http.Handle("/api", http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			key := r.URL.Query().Get("key")
			view, err := gee.Get(key)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(view.ByteSlice())

		}))
	log.Println("fontend server is running at", apiAddr)
	log.Fatal(http.ListenAndServe(apiAddr[7:], nil))

}
func createGroup() *peanutcache.Group {
	return peanutcache.NewGroup("scores", 2<<10, peanutcache.RetrieverFunc(
		func(key string) ([]byte, error) {
			log.Println("[Mysql] search key", key)
			if v, ok := mysql[key]; ok {
				return []byte(v), nil
			}
			return nil, fmt.Errorf("%s not exist", key)
		}))
}

func main() {
	var port int
	var api bool
	flag.IntVar(&port, "port", 8001, "Geecache server port")
	flag.BoolVar(&api, "api", false, "Start a api server?")
	flag.Parse()

	apiAddr := "http://localhost:9999"
	addrMap := map[int]string{
		8001: "127.0.0.1:8001",
		8002: "127.0.0.1:8002",
		8003: "127.0.0.1:8003",
	}

	// var addrs []string
	// for _, v := range addrMap {
	// 	addrs = append(addrs, v)
	// }

	// 新建cache实例
	group := createGroup()
	//启动api server
	if api {
		go startAPIServer(apiAddr, group)
	}

	// New一个服务实例
	var addr string = addrMap[port]
	svr, err := peanutcache.NewServer(addr)
	if err != nil {
		log.Fatal(err)
	}
	// 设置同伴节点IP(包括自己)
	// 这里的peer地址从etcd获取(服务发现)
	svr.SetPeers()
	// 将服务与cache绑定 因为cache和server是解耦合的
	group.RegisterSvr(svr)
	log.Println("peanutcache is running at", addr)
	// 启动服务(注册服务至etcd/计算一致性哈希...)

	// Start将不会return 除非服务stop或者抛出error
	err = svr.Start()
	if err != nil {
		log.Fatal(err)
	}

}
```
Before `go run`, you should run `etcd` local directly(without any spcified parameter) and then execute `go run example.go`, you will get follows:

```console
$ go run main.go -port=8001 -api=1
$ go run main.go -port=8002
$ go run main.go -port=8003

etcdctl get --prefix ""
peanutcache/127.0.0.1:8001/127.0.0.1:8001
{"Op":0,"Addr":"127.0.0.1:8001","Metadata":null}
peanutcache/127.0.0.1:8002/127.0.0.1:8002
{"Op":0,"Addr":"127.0.0.1:8002","Metadata":null}
peanutcache/127.0.0.1:8003/127.0.0.1:8003
{"Op":0,"Addr":"127.0.0.1:8003","Metadata":null}

curl http://localhost:9999/api?key=Sam
2024/07/25 16:16:38 peanutcache is running at 127.0.0.1:8002
2024/07/25 16:16:38 [127.0.0.1:8002] register service ok
[127.0.0.1:8002] register service ok
[peanutcache_svr 127.0.0.1:8002] Recv RPC Request - (scores)/(Sam)2024/07/25 16:18:25 ooh! pick myself, I am 127.0.0.1:8002
2024/07/25 16:18:25 [Mysql] search key Sam
[peanutcache_svr 127.0.0.1:8002] Recv RPC Request - (scores)/(Sam)2024/07/25 16:19:57 cache hit

```

