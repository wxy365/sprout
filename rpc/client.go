package rpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"reflect"
	"time"

	"github.com/wxy365/basal/errs"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Client 是一个 gRPC 客户端。它持有一个到目标服务的连接。
//
// See client.go for the implementation.
type Client struct {
	// service 是目标服务的全限定名。
	service string
	// conn 是到目标服务的连接。
	conn *grpc.ClientConn
	// timeout 是单次调用的默认超时，0 表示不设超时。
	timeout time.Duration
	// descCache 缓存消息描述符，避免每次调用都重新合成。
	descCache map[string]protoreflect.MessageDescriptor
}

// clientOptions 是客户端的配置。
type clientOptions struct {
	// direct 是直连的地址。
	direct string
	// registry 是服务注册中心，优先于直连地址。
	registry Registry
	// nacos 是 Nacos 注册中心的配置。
	nacos *NacosConfig
	// registryService 是注册中心侧的服务名，默认与 gRPC 服务名相同。
	registryService string
	// balancer 是负载均衡策略。
	balancer string
	// tlsConfig 是 TLS 配置，为 nil 时使用明文连接。
	tlsConfig *tls.Config
	// timeout 是单次调用的默认超时。
	timeout time.Duration
	// metadata 是每次调用都附加的元数据。
	metadata map[string][]string
	// unaryInterceptor 是一元调用的客户端拦截器。
	unaryInterceptor grpc.UnaryClientInterceptor
	// streamInterceptor 是流调用的客户端拦截器。
	streamInterceptor grpc.StreamClientInterceptor
}

// ClientOption 配置客户端。选项按给出的顺序应用。
//
// See client.go for the implementation.
type ClientOption func(*clientOptions)

// WithDirect 让客户端直连给定的地址。
//
// The exported wrapper WithDirect is declared in api.go, which collects the
// public API surface of the package.
func withDirect(addr string) ClientOption {
	return func(o *clientOptions) {
		o.direct = addr
	}
}

// WithBalancer 设置负载均衡策略，默认 round_robin。
//
// The exported wrapper WithBalancer is declared in api.go, which collects the
// public API surface of the package.
func withBalancer(name string) ClientOption {
	return func(o *clientOptions) {
		o.balancer = name
	}
}

// WithTimeout 设置单次调用的默认超时。
//
// The exported wrapper WithTimeout is declared in api.go, which collects the
// public API surface of the package.
func withTimeout(d time.Duration) ClientOption {
	return func(o *clientOptions) {
		o.timeout = d
	}
}

// WithTLS 设置 TLS 配置。默认使用明文连接。
//
// The exported wrapper WithTLS is declared in api.go, which collects the
// public API surface of the package.
func withTLS(cfg *tls.Config) ClientOption {
	return func(o *clientOptions) {
		o.tlsConfig = cfg
	}
}

// WithMetadata 设置每次调用都附加的元数据。
//
// The exported wrapper WithMetadata is declared in api.go, which collects the
// public API surface of the package.
func withMetadata(md map[string][]string) ClientOption {
	return func(o *clientOptions) {
		o.metadata = md
	}
}

// WithUnaryInterceptor 设置一元调用的客户端拦截器。
//
// The exported wrapper WithUnaryInterceptor is declared in api.go, which
// collects the public API surface of the package.
func withUnaryInterceptor(i grpc.UnaryClientInterceptor) ClientOption {
	return func(o *clientOptions) {
		o.unaryInterceptor = i
	}
}

// WithStreamInterceptor 设置流调用的客户端拦截器。
//
// The exported wrapper WithStreamInterceptor is declared in api.go, which
// collects the public API surface of the package.
func withStreamInterceptor(i grpc.StreamClientInterceptor) ClientOption {
	return func(o *clientOptions) {
		o.streamInterceptor = i
	}
}

// newClientOptions 按给定的选项构建客户端配置。
func newClientOptions(opts ...ClientOption) *clientOptions {
	o := &clientOptions{balancer: "round_robin"}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return o
}

// NewClient 创建一个针对指定 gRPC 服务的客户端。
// 未指定寻址方式时，必须用 WithDirect 指定地址，或者用 WithRegistry /
// WithNacos 指定服务注册中心。
//
// The exported wrapper NewClient is declared in api.go, which collects the
// public API surface of the package.
func newClient(service string, opts ...ClientOption) (*Client, error) {
	o := newClientOptions(opts...)
	dialOpts := []grpc.DialOption{
		grpc.WithDefaultServiceConfig(fmt.Sprintf(`{"loadBalancingConfig":[{"%s":{}}]}`, o.balancer)),
	}
	if o.tlsConfig != nil {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(o.tlsConfig)))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	if o.unaryInterceptor != nil {
		dialOpts = append(dialOpts, grpc.WithUnaryInterceptor(o.unaryInterceptor))
	}
	if o.streamInterceptor != nil {
		dialOpts = append(dialOpts, grpc.WithStreamInterceptor(o.streamInterceptor))
	}

	if o.nacos != nil {
		reg, err := newNacosRegistry(*o.nacos)
		if err != nil {
			return nil, err
		}
		o.registry = reg
	}

	var target string
	if o.registry != nil {
		name := o.registryService
		if name == "" {
			name = service
		}
		target = registryTarget(name)
		dialOpts = append(dialOpts, grpc.WithResolvers(&registryResolverBuilder{registry: o.registry}))
	} else {
		if o.direct == "" {
			return nil, errs.New("The client of the service [{0}] has no address", service)
		}
		target = "passthrough:///" + o.direct
	}

	conn, err := grpc.NewClient(target, dialOpts...)
	if err != nil {
		return nil, errs.Wrap(err, "Cannot connect to the service [{0}]", service)
	}
	return &Client{
		service:  service,
		conn:     conn,
		timeout:  o.timeout,
		descCache: map[string]protoreflect.MessageDescriptor{},
	}, nil
}

// Close 关闭连接。
//
// See client.go for the implementation.
func (c *Client) Close() error {
	return c.conn.Close()
}

// messageDescs 合成并缓存方法的入参与出参描述符。
func (c *Client) messageDescs(method string, inType, outType reflect.Type) (protoreflect.MessageDescriptor, protoreflect.MessageDescriptor, error) {
	pkg, _, err := servicePackage(c.service)
	if err != nil {
		return nil, nil, err
	}
	key := pkg + "." + inType.Name() + "/" + pkg + "." + outType.Name()
	if inDesc, ok := c.descCache[key]; ok {
		return inDesc, c.descCache[key+"#out"], nil
	}
	_, msgs, err := buildFileDescriptor(pkg, pkg+"/"+serviceFileName(c.service)+".proto", []reflect.Type{inType, outType})
	if err != nil {
		return nil, nil, err
	}
	inDesc := msgs[pkg+"."+inType.Name()]
	outDesc := msgs[pkg+"."+outType.Name()]
	if inDesc == nil || outDesc == nil {
		return nil, nil, errs.New("The message descriptors of the method [{0}] are not found", method)
	}
	c.descCache[key] = inDesc
	c.descCache[key+"#out"] = outDesc
	return inDesc, outDesc, nil
}

// callContext 按客户端的配置补齐调用上下文。
func (c *Client) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.timeout > 0 {
		return context.WithTimeout(ctx, c.timeout)
	}
	return ctx, func() {}
}

// Doer 返回一个类型安全的一元调用闭包，与 sprout/cli.Doer 的风格一致。
//
// The exported wrapper Doer is declared in api.go, which collects the public
// API surface of the package.
func doer[I, O any](c *Client, method string) func(ctx context.Context, in *I) (*O, error) {
	return func(ctx context.Context, in *I) (*O, error) {
		inType := reflect.TypeOf((*I)(nil)).Elem()
		outType := reflect.TypeOf((*O)(nil)).Elem()
		inDesc, outDesc, err := c.messageDescs(method, inType, outType)
		if err != nil {
			return nil, err
		}
		ctx, cancel := c.callContext(ctx)
		defer cancel()

		inMsg := newDynamicMessage(inDesc)
		if err := toMessage(reflect.ValueOf(in).Elem(), inMsg); err != nil {
			return nil, err
		}
		outMsg := newDynamicMessage(outDesc)
		fullMethod := "/" + c.service + "/" + method
		if err := c.conn.Invoke(ctx, fullMethod, inMsg, outMsg); err != nil {
			return nil, fromStatus(err)
		}
		out := new(O)
		if err := fromMessage(outMsg, reflect.ValueOf(out).Elem()); err != nil {
			return nil, err
		}
		return out, nil
	}
}

// StreamDoer 返回一个类型安全的双向流调用闭包，与 sprout/cli.Doer 的风格一致。
//
// The exported wrapper StreamDoer is declared in api.go, which collects the
// public API surface of the package.
func streamDoer[I, O any](c *Client, method string) func(ctx context.Context) (*ClientStream[I, O], error) {
	return func(ctx context.Context) (*ClientStream[I, O], error) {
		inType := reflect.TypeOf((*I)(nil)).Elem()
		outType := reflect.TypeOf((*O)(nil)).Elem()
		inDesc, outDesc, err := c.messageDescs(method, inType, outType)
		if err != nil {
			return nil, err
		}
		ctx, cancel := c.callContext(ctx)
		fullMethod := "/" + c.service + "/" + method
		stream, err := c.conn.NewStream(ctx, &grpc.StreamDesc{
			StreamName:    method,
			ServerStreams: true,
			ClientStreams: true,
		}, fullMethod)
		if err != nil {
			cancel()
			return nil, fromStatus(err)
		}
		return &ClientStream[I, O]{stream: stream, inDesc: inDesc, outDesc: outDesc, cancel: cancel}, nil
	}
}
