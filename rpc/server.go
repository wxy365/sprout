package rpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/wxy365/basal/cfg/def"
	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/log"
	sp "github.com/wxy365/sprout"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Server 是一个 gRPC 服务端实例。它随应用一起启动，随应用一起停止。
//
// See server.go for the implementation.
type Server struct {
	// Name 是服务端名称，用于挂载服务与配置匹配。
	Name string
	// Port 是监听端口，0 表示由系统分配。
	Port uint16
	// CertFile 是 TLS 证书文件路径，与 KeyFile 同时设置时启用 TLS。
	CertFile string
	// KeyFile 是 TLS 私钥文件路径。
	KeyFile string
	// Debug 打开方法级的调试日志。
	Debug bool
	// ShutdownTimeout 是优雅停止的等待时长。
	ShutdownTimeout time.Duration

	mu sync.Mutex
	// services 是挂载到该服务端的服务。
	services []*Service
	// server 是底层的 grpc 服务端。
	server *grpc.Server
}

// serverCfg 是 app.grpc_servers 配置项。
type serverCfg struct {
	Name            string `map:"name"`
	Port            uint16 `map:"port"`
	CertFile        string `map:"cert_file"`
	KeyFile         string `map:"key_file"`
	Debug           *bool  `map:"debug"`
	ShutdownTimeout uint64 `map:"shutdown_timeout"`
}

// NewServer 创建一个 gRPC 服务端实例，并从 app.grpc_servers 配置补全同名项。
//
// The exported wrapper NewServer is declared in api.go, which collects the
// public API surface of the package.
func newServer(name string) *Server {
	s := &Server{Name: name, ShutdownTimeout: 10 * time.Second}
	s.applyConfig()
	return s
}

// ServersFromConfig 按 app.grpc_servers 配置创建全部 gRPC 服务端实例。
//
// The exported wrapper ServersFromConfig is declared in api.go, which collects
// the public API surface of the package.
func serversFromConfig() []*Server {
	cfgs, err := def.GetObj[[]serverCfg]("app.grpc_servers")
	if err != nil {
		return nil
	}
	servers := make([]*Server, 0, len(cfgs))
	for _, c := range cfgs {
		if c.Name == "" {
			log.Panic("The gRPC server name must not be empty! Please check the configuration files.")
		}
		servers = append(servers, newServer(c.Name))
	}
	return servers
}

// applyConfig 用配置里的同名项补全该服务端。
func (s *Server) applyConfig() {
	cfgs, err := def.GetObj[[]serverCfg]("app.grpc_servers")
	if err != nil {
		return
	}
	for _, c := range cfgs {
		if c.Name != s.Name {
			continue
		}
		if c.Port > 0 {
			s.Port = c.Port
		}
		if c.CertFile != "" {
			s.CertFile = c.CertFile
		}
		if c.KeyFile != "" {
			s.KeyFile = c.KeyFile
		}
		if c.Debug != nil {
			s.Debug = *c.Debug
		}
		if c.ShutdownTimeout > 0 {
			s.ShutdownTimeout = time.Millisecond * time.Duration(c.ShutdownTimeout)
		}
	}
}

// addService 挂载一个服务到该服务端。
func (s *Server) addService(svc *Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.services = append(s.services, svc)
}

// buildGrpcServer 构建 grpc 服务端并注册全部服务。
func (s *Server) buildGrpcServer() (*grpc.Server, error) {
	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(unaryInterceptors()...),
		grpc.ChainStreamInterceptor(streamInterceptors()...),
	}
	if s.CertFile != "" && s.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(s.CertFile, s.KeyFile)
		if err != nil {
			return nil, errs.Wrap(err, "Cannot load the TLS key pair of the gRPC server [{0}]", s.Name)
		}
		opts = append(opts, grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}})))
	}
	gs := grpc.NewServer(opts...)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, svc := range s.services {
		desc, err := svc.buildServiceDesc()
		if err != nil {
			return nil, errs.Wrap(err, "Cannot build the service descriptor of the gRPC server [{0}]", s.Name)
		}
		gs.RegisterService(desc, struct{}{})
	}
	return gs, nil
}

// serve 在给定的监听器上提供服务，直到服务停止。
func (s *Server) serve(lis net.Listener) {
	gs, err := s.buildGrpcServer()
	if err != nil {
		log.ErrorErrF("Cannot build the gRPC server [{0}]", err, s.Name)
		return
	}
	s.mu.Lock()
	s.server = gs
	s.mu.Unlock()
	if err := gs.Serve(lis); err != nil {
		log.ErrorErrF("The gRPC server [{0}] stopped with an error", err, s.Name)
	}
}

// Start 启动该服务端并阻塞，直到服务端停止。
func (s *Server) Start() error {
	gs, err := s.buildGrpcServer()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.server = gs
	s.mu.Unlock()

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		return errs.Wrap(err, "Cannot listen on the port [{0}] of the gRPC server [{1}]", s.Port, s.Name)
	}
	if addr, ok := lis.Addr().(*net.TCPAddr); ok {
		s.mu.Lock()
		s.Port = uint16(addr.Port)
		s.mu.Unlock()
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		ctx, cancel := context.WithTimeout(context.Background(), s.ShutdownTimeout)
		defer cancel()
		s.Stop(ctx)
	}()

	log.Info("The gRPC server [{0}] is started on port: {1}", s.Name, s.Port)
	if err := gs.Serve(lis); err != nil {
		return err
	}
	return nil
}

// Stop 优雅停止该服务端。超过优雅停止的等待时长则强制停止。
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	gs := s.server
	s.mu.Unlock()
	if gs == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		gs.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		gs.Stop()
		return ctx.Err()
	}
}

// Mount 把一个 gRPC 服务挂载到应用中指定的 gRPC 服务端上。
// 与 sprout 的 Mount 语义一致：未指定服务端名时挂到第一个 gRPC 服务端。
//
// The exported wrapper Mount is declared in api.go, which collects the public
// API surface of the package.
func mount[C any](svc *Service, a *sp.App[C], svrName ...string) {
	a.Initializers = append(a.Initializers, func(app *sp.App[C]) error {
		target, err := findServer(app, svc.Name, svrName...)
		if err != nil {
			return err
		}
		target.addService(svc)
		return nil
	})
}

// findServer 在应用的运行组件中查找 gRPC 服务端。
func findServer[C any](app *sp.App[C], serviceName string, svrName ...string) (*Server, error) {
	for _, r := range app.Runners {
		srv, ok := r.(*Server)
		if !ok {
			continue
		}
		if len(svrName) == 0 {
			return srv, nil
		}
		if srv.Name == svrName[0] {
			return srv, nil
		}
	}
	if len(svrName) == 0 {
		return nil, errs.New("Failed to mount the service [{0}]: the application has no gRPC server", serviceName)
	}
	return nil, errs.New("Failed to mount the service to the gRPC server [{0}]: the server is not defined", svrName[0])
}
