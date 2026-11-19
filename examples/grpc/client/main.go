// Command client calls the gRPC example server.
//
// It demonstrates how the type safe client of the sprout/rpc package is
// used: the unary call and the bidirectional stream are both closures that
// are typed by their request and response messages.
//
// Run it with:
//
//	go run ./examples/grpc/client
//
// The client connects to 127.0.0.1:9090 by default. Set the GRPC_ADDR
// environment variable to change it.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	sproutrpc "github.com/wxy365/sprout/rpc"

	"github.com/wxy365/sprout/examples/grpc/messages"
)

func main() {
	addr := os.Getenv("GRPC_ADDR")
	if addr == "" {
		addr = "127.0.0.1:9090"
	}

	client, err := sproutrpc.NewClient("demo.Greeter",
		sproutrpc.WithDirect(addr),
		sproutrpc.WithTimeout(5*time.Second))
	if err != nil {
		fmt.Println("Cannot create the client:", err)
		os.Exit(1)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The unary call.
	hello := sproutrpc.Doer[messages.HelloReq, messages.HelloResp](client, "Hello")
	resp, err := hello(ctx, &messages.HelloReq{Name: "wxy", Age: 30})
	if err != nil {
		fmt.Println("The unary call failed:", err)
		os.Exit(1)
	}
	fmt.Println("unary ->", resp.Greeting)

	// The bidirectional stream.
	chat := sproutrpc.StreamDoer[messages.ChatReq, messages.ChatResp](client, "Chat")
	stream, err := chat(ctx)
	if err != nil {
		fmt.Println("Cannot open the stream:", err)
		os.Exit(1)
	}
	for _, text := range []string{"hello", "grpc"} {
		if err := stream.Send(&messages.ChatReq{Text: text}); err != nil {
			fmt.Println("Cannot send the message:", err)
			os.Exit(1)
		}
	}
	if err := stream.CloseSend(); err != nil {
		fmt.Println("Cannot close the send direction:", err)
		os.Exit(1)
	}
	for {
		out, err := stream.Recv()
		if err == io.EOF {
			fmt.Println("the stream is closed")
			break
		}
		if err != nil {
			fmt.Println("Cannot receive the message:", err)
			os.Exit(1)
		}
		fmt.Println("stream ->", out.Text)
	}
}
