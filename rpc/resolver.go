package rpc

import (
	"context"
	"net/url"
	"strings"
	"sync"

	"google.golang.org/grpc/resolver"
)

// registryScheme 是走注册中心寻址时使用的目标 scheme。
const registryScheme = "sprout"

// Instance 是一个服务提供者实例。
//
// See resolver.go for the implementation.
type Instance struct {
	// Addr 是实例地址，形如 host:port。
	Addr string
	// Healthy 表示实例是否健康。
	Healthy bool
	// Weight 是实例权重。
	Weight float64
	// Metadata 是实例元数据。
	Metadata map[string]string
}

// Registry 把服务名解析为服务提供者实例列表。
//
// See resolver.go for the implementation.
type Registry interface {
	// Discover 订阅指定服务的实例列表，返回实例更新的通道。通道关闭表示订阅结束。
	Discover(ctx context.Context, service string) (<-chan []Instance, error)
}

// WithRegistry 让客户端通过给定的注册中心发现服务提供者。
//
// The exported wrapper WithRegistry is declared in api.go, which collects the
// public API surface of the package.
func withRegistry(r Registry) ClientOption {
	return func(o *clientOptions) {
		o.registry = r
	}
}

// WithRegistryService 设置注册中心侧的服务名，默认与 gRPC 服务名相同。
//
// The exported wrapper WithRegistryService is declared in api.go, which
// collects the public API surface of the package.
func withRegistryService(name string) ClientOption {
	return func(o *clientOptions) {
		o.registryService = name
	}
}

// registryTarget 构造走注册中心的目标串。
func registryTarget(service string) string {
	return registryScheme + ":///" + url.PathEscape(service)
}

// registryResolverBuilder 把注册中心的实例列表推送给 grpc-go。
type registryResolverBuilder struct {
	// registry 是实例列表的来源。
	registry Registry
}

// Scheme 返回该 builder 支持的 scheme。
func (b *registryResolverBuilder) Scheme() string {
	return registryScheme
}

// Build 为一个目标构建 resolver。
func (b *registryResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	service := strings.TrimPrefix(target.URL.Path, "/")
	if service == "" {
		service = strings.TrimPrefix(target.URL.String(), "/")
	}
	if v := target.URL.Query().Get("service"); v != "" {
		service = v
	}
	ctx, cancel := context.WithCancel(context.Background())
	instances, err := b.registry.Discover(ctx, service)
	if err != nil {
		cancel()
		return nil, err
	}
	r := &registryResolver{cc: cc, cancel: cancel}
	go r.watch(ctx, instances)
	return r, nil
}

// registryResolver 把注册中心的实例变化转成 grpc 的地址更新。
type registryResolver struct {
	// cc 是 grpc 的客户端连接。
	cc resolver.ClientConn
	// cancel 停止订阅。
	cancel context.CancelFunc
	// mu 保护下面的状态。
	mu sync.Mutex
	// last 是上一次推送的地址列表。
	last []resolver.Address
}

// watch 读取实例更新并推送给 grpc-go。
func (r *registryResolver) watch(ctx context.Context, instances <-chan []Instance) {
	for {
		select {
		case <-ctx.Done():
			return
		case list, ok := <-instances:
			if !ok {
				return
			}
			r.update(list)
		}
	}
}

// update 把实例列表推送给 grpc-go，列表未变化时跳过。
func (r *registryResolver) update(list []Instance) {
	addrs := toResolverAddresses(list)
	r.mu.Lock()
	same := len(addrs) == len(r.last)
	if same {
		for i := range addrs {
			if addrs[i].Addr != r.last[i].Addr {
				same = false
				break
			}
		}
	}
	if !same {
		r.last = addrs
	}
	r.mu.Unlock()
	if same {
		return
	}
	r.cc.UpdateState(resolver.State{Addresses: addrs})
}

// ResolveNow 提示重新解析。实例列表由注册中心推送驱动，因此这里不做处理。
func (r *registryResolver) ResolveNow(resolver.ResolveNowOptions) {
}

// Close 停止订阅。
func (r *registryResolver) Close() {
	r.cancel()
}

// toResolverAddresses 把实例列表转成 grpc 的地址列表。
func toResolverAddresses(list []Instance) []resolver.Address {
	addrs := make([]resolver.Address, 0, len(list))
	for _, i := range list {
		if !i.Healthy {
			continue
		}
		addrs = append(addrs, resolver.Address{Addr: i.Addr})
	}
	return addrs
}
