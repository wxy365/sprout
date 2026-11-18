package rpc

import (
	"context"
	"crypto/tls"
	"time"

	sp "github.com/wxy365/sprout"
	"google.golang.org/grpc"
)

// This file collects the public API surface of the package, so that consumers
// have a single, stable entry point to discover what the package offers.
// The implementations stay in the files where they belong, and the functions
// below are thin re-exports of them.

// Unary constructs a unary method. I is the request type and O is the
// response type.
//
// See service.go for the implementation.
func Unary[I, O any](name string, handler func(ctx *Context, in *I) (*O, error)) Method {
	return unary[I, O](name, handler)
}

// Stream constructs a bidirectional streaming method. I is the request type
// and O is the response type.
//
// See stream.go for the implementation.
func Stream[I, O any](name string, handler func(ctx *Context, stream *ServerStream[I, O]) error) Method {
	return stream[I, O](name, handler)
}

// Mount mounts a gRPC service onto one of the gRPC servers of the
// application. If no server name is given, the service is mounted on the
// first gRPC server of the App.Runners slice.
//
// See server.go for the implementation.
func Mount[C any](svc *Service, a *sp.App[C], svrName ...string) {
	mount[C](svc, a, svrName...)
}

// NewServer creates a gRPC server instance and completes it with the
// settings of the same name in the app.grpc_servers configuration.
//
// See server.go for the implementation.
func NewServer(name string) *Server {
	return newServer(name)
}

// ServersFromConfig creates all the gRPC server instances declared in the
// app.grpc_servers configuration.
//
// See server.go for the implementation.
func ServersFromConfig() []*Server {
	return serversFromConfig()
}

// NewClient creates a client for the given gRPC service. The address must be
// given with WithDirect, or the service registry must be given with
// WithRegistry or WithNacos.
//
// See client.go for the implementation.
func NewClient(service string, opts ...ClientOption) (*Client, error) {
	return newClient(service, opts...)
}

// Doer returns a type safe closure for a unary call, in the same style as
// sprout/cli.Doer.
//
// See client.go for the implementation.
func Doer[I, O any](c *Client, method string) func(ctx context.Context, in *I) (*O, error) {
	return doer[I, O](c, method)
}

// StreamDoer returns a type safe closure for a bidirectional streaming call.
//
// See client.go for the implementation.
func StreamDoer[I, O any](c *Client, method string) func(ctx context.Context) (*ClientStream[I, O], error) {
	return streamDoer[I, O](c, method)
}

// WithDirect makes the client connect to the given address directly.
//
// See client.go for the implementation.
func WithDirect(addr string) ClientOption {
	return withDirect(addr)
}

// WithBalancer sets the load balancing policy, round_robin by default.
//
// See client.go for the implementation.
func WithBalancer(name string) ClientOption {
	return withBalancer(name)
}

// WithTimeout sets the default timeout of a single call.
//
// See client.go for the implementation.
func WithTimeout(d time.Duration) ClientOption {
	return withTimeout(d)
}

// WithTLS sets the TLS configuration. The connection is in plaintext by
// default.
//
// See client.go for the implementation.
func WithTLS(cfg *tls.Config) ClientOption {
	return withTLS(cfg)
}

// WithMetadata sets the metadata that is attached to every call.
//
// See client.go for the implementation.
func WithMetadata(md map[string][]string) ClientOption {
	return withMetadata(md)
}

// WithUnaryInterceptor sets the client interceptor of the unary calls.
//
// See client.go for the implementation.
func WithUnaryInterceptor(i grpc.UnaryClientInterceptor) ClientOption {
	return withUnaryInterceptor(i)
}

// WithStreamInterceptor sets the client interceptor of the streaming calls.
//
// See client.go for the implementation.
func WithStreamInterceptor(i grpc.StreamClientInterceptor) ClientOption {
	return withStreamInterceptor(i)
}

// WithRegistry makes the client discover the service providers through the
// given service registry.
//
// See resolver.go for the implementation.
func WithRegistry(r Registry) ClientOption {
	return withRegistry(r)
}

// WithRegistryService sets the service name on the registry side, which
// defaults to the gRPC service name.
//
// See resolver.go for the implementation.
func WithRegistryService(name string) ClientOption {
	return withRegistryService(name)
}

// WithNacos makes the client discover the service providers through Nacos.
//
// See nacos.go for the implementation.
func WithNacos(cfg NacosConfig) ClientOption {
	return withNacos(cfg)
}

// NewNacosRegistry creates a Nacos service registry.
//
// See nacos.go for the implementation.
func NewNacosRegistry(cfg NacosConfig) (Registry, error) {
	return newNacosRegistry(cfg)
}
