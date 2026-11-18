package rpc

import (
	"context"
	"sync"

	"github.com/wxy365/basal/log"
	sp "github.com/wxy365/sprout"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryInterceptor 拦截一元调用。
//
// See interceptor.go for the implementation.
type UnaryInterceptor = grpc.UnaryServerInterceptor

// StreamInterceptor 拦截流式调用。
//
// See interceptor.go for the implementation.
type StreamInterceptor = grpc.StreamServerInterceptor

// clientIDMetadataKey 是承载客户端标识的元数据键。
const clientIDMetadataKey = "client-id"

// recoverUnaryInterceptor 把处理函数的 panic 转成 Internal 错误。
func recoverUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("Panic recovered in the gRPC method: {0} {1}", info.FullMethod, r)
				err = status.Error(codes.Internal, "Internal Server Error")
			}
		}()
		return handler(ctx, req)
	}
}

// recoverStreamInterceptor 把流处理函数的 panic 转成 Internal 错误。
func recoverStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("Panic recovered in the gRPC stream: {0} {1}", info.FullMethod, r)
				err = status.Error(codes.Internal, "Internal Server Error")
			}
		}()
		return handler(srv, ss)
	}
}

// breakerUnaryInterceptor 按方法名做熔断，配置取自 app.breakers。
func breakerUnaryInterceptor() grpc.UnaryServerInterceptor {
	var guards sync.Map
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		guard, ok := guards.Load(info.FullMethod)
		if !ok {
			created := sp.NewCircuitBreakerGuard(info.FullMethod)
			guard, _ = guards.LoadOrStore(info.FullMethod, created)
		}
		done, allowErr := guard.(*sp.CircuitBreakerGuard).Allow()
		if allowErr != nil {
			return nil, status.Error(codes.Unavailable, "the circuit breaker is open")
		}
		var panicked bool
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				done(false)
				panic(r)
			}
			if panicked {
				return
			}
			done(err == nil)
		}()
		return handler(ctx, req)
	}
}

// limiterUnaryInterceptor 按方法名做限流，配置取自 app.limiters。
func limiterUnaryInterceptor() grpc.UnaryServerInterceptor {
	var guards sync.Map
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		guard, ok := guards.Load(info.FullMethod)
		if !ok {
			created := sp.NewRateLimiterGuard(info.FullMethod)
			guard, _ = guards.LoadOrStore(info.FullMethod, created)
		}
		if !guard.(*sp.RateLimiterGuard).Allow(clientIdentityFromContext(ctx)) {
			return nil, status.Error(codes.ResourceExhausted, "the request is rate limited")
		}
		return handler(ctx, req)
	}
}

// clientIdentityFromContext 从调用方传入的元数据里取客户端标识。
func clientIdentityFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get(clientIDMetadataKey)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// unaryInterceptors 返回服务端的一元拦截器链。
func unaryInterceptors() []grpc.UnaryServerInterceptor {
	return []grpc.UnaryServerInterceptor{
		recoverUnaryInterceptor(),
		breakerUnaryInterceptor(),
		limiterUnaryInterceptor(),
	}
}

// streamInterceptors 返回服务端的流拦截器链。
func streamInterceptors() []grpc.StreamServerInterceptor {
	return []grpc.StreamServerInterceptor{
		recoverStreamInterceptor(),
	}
}
