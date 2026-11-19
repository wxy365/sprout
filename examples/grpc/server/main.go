// Command server runs the gRPC example server.
//
// It demonstrates how a service is defined with plain Go structs and plain Go
// functions: no .proto file and no generated code are involved.
//
// Run it with:
//
//	go run ./examples/grpc/server
//
// The server listens on the port 9090 by default. Set the GRPC_PORT
// environment variable to change it.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/log"
	"github.com/wxy365/sprout"
	sproutrpc "github.com/wxy365/sprout/rpc"

	"github.com/wxy365/sprout/examples/grpc/messages"
)

// appCtx is the application context of the example.
type appCtx struct{}

// hello greets the guest.
func hello(ctx *sproutrpc.Context, in *messages.HelloReq) (*messages.HelloResp, error) {
	if in.Name == "" {
		return nil, errs.New("The name must not be empty").WithStatus(400)
	}
	return &messages.HelloResp{Greeting: fmt.Sprintf("hello %s (%d)", in.Name, in.Age)}, nil
}

// chat echoes every message back to the caller.
func chat(ctx *sproutrpc.Context, s *sproutrpc.ServerStream[messages.ChatReq, messages.ChatResp]) error {
	for {
		in, err := s.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.Send(&messages.ChatResp{Text: "echo " + in.Text}); err != nil {
			return err
		}
	}
}

func main() {
	port := uint16(9090)
	if v := os.Getenv("GRPC_PORT"); v != "" {
		p, err := strconv.ParseUint(v, 10, 16)
		if err != nil {
			log.Panic("The port [{0}] is not a number", v)
		}
		port = uint16(p)
	}

	srv := sproutrpc.NewServer("grpc")
	srv.Port = port

	// The gRPC server is started and stopped together with the application.
	app := &sprout.App[*appCtx]{
		Name:    "grpc-example",
		Runners: []sprout.Runner{srv},
	}
	sproutrpc.Mount(&sproutrpc.Service{
		Name: "demo.Greeter",
		Methods: []sproutrpc.Method{
			sproutrpc.Unary("Hello", hello),
			sproutrpc.Stream("Chat", chat),
		},
	}, app, "grpc")

	// Stop the server gracefully once the process is asked to quit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Stop(stopCtx); err != nil {
			log.ErrorErr(err)
		}
	}()

	log.Info("The gRPC example server is starting on the port: {0}", port)
	app.Run()
	log.Info("The gRPC example server is stopped")
}
