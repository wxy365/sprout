package rpc

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"
)

// startExampleServer 启动一个只监听本机的 gRPC 服务端，返回监听地址与停止函数。
// Example 函数不能拿到 testing.T，所以这里不复用 startTestServer。
func startExampleServer(svc *Service) (string, func()) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	srv := &Server{Name: "example", ShutdownTimeout: 2 * time.Second}
	srv.addService(svc)
	go srv.serve(lis)
	return lis.Addr().String(), func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Stop(ctx)
	}
}

// Example 演示如何定义一个 gRPC 服务，以及如何用类型安全的客户端调用它。
//
// 服务端与客户端跑在同一个进程里，但走的是真实的 TCP 端口，所以这个示例
// 同时也是一次端到端的验证。消息是带 pb 标签的普通 Go 结构体，既不需要
// .proto 文件，也不需要 protoc。
func Example() {
	svc := &Service{
		Name: "demo.Greeter",
		Methods: []Method{
			unary("Hello", func(ctx *Context, in *svcHelloReq) (*svcHelloResp, error) {
				return &svcHelloResp{Greeting: "hello " + in.Name}, nil
			}),
			stream("Chat", func(ctx *Context, s *ServerStream[streamChatReq, streamChatResp]) error {
				for {
					in, err := s.Recv()
					if err == io.EOF {
						return nil
					}
					if err != nil {
						return err
					}
					if err := s.Send(&streamChatResp{Text: "echo " + in.Text}); err != nil {
						return err
					}
				}
			}),
		},
	}
	addr, stop := startExampleServer(svc)
	defer stop()

	client, err := newClient("demo.Greeter", withDirect(addr))
	if err != nil {
		fmt.Println("cannot create the client:", err)
		return
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 一元调用。
	hello := doer[svcHelloReq, svcHelloResp](client, "Hello")
	resp, err := hello(ctx, &svcHelloReq{Name: "wxy"})
	if err != nil {
		fmt.Println("the unary call failed:", err)
		return
	}
	fmt.Println("unary ->", resp.Greeting)

	// 双向流式调用。
	chat := streamDoer[streamChatReq, streamChatResp](client, "Chat")
	stream, err := chat(ctx)
	if err != nil {
		fmt.Println("cannot open the stream:", err)
		return
	}
	if err := stream.Send(&streamChatReq{Text: "hello"}); err != nil {
		fmt.Println("cannot send the message:", err)
		return
	}
	if err := stream.CloseSend(); err != nil {
		fmt.Println("cannot close the send direction:", err)
		return
	}
	for {
		out, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("cannot receive the message:", err)
			return
		}
		fmt.Println("stream ->", out.Text)
	}

	// Output:
	// unary -> hello wxy
	// stream -> echo hello
}
