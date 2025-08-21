package sprout

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/wxy365/basal/log"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type Server struct {
	Name            string
	Port            uint16
	CertFile        string
	KeyFile         string
	Debug           bool
	ShutdownTimeout time.Duration

	Validators   []Validator
	ErrorHandler ErrorHandler

	endpoints []*refinedEndpoint
}

func newDefaultServer(name string) *Server {
	return &Server{
		Name:            name,
		Debug:           false,
		ShutdownTimeout: 10 * time.Second,
		Validators:      defaultValidators(),
		ErrorHandler:    defaultErrHandler,
	}
}

func (s *Server) start() {
	mx := s.buildMux()
	var gracefulShutdown func(timeout time.Duration)
	if s.CertFile == "" || s.KeyFile == "" {
		if s.Port == 0 {
			s.Port = 80
		}
		server := &http.Server{
			Addr:    fmt.Sprintf(":%d", s.Port),
			Handler: h2c.NewHandler(mx, &http2.Server{}),
		}
		gracefulShutdown = func(timeout time.Duration) {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			err := server.Shutdown(ctx)
			if err != nil {
				log.PanicErrF("Failed to shutdown sprout server gracefully", err)
			}
		}

		fmt.Printf("'%s' is started on port: %d\n", s.Name, s.Port)

		err := server.ListenAndServe()
		if err != nil {
			server.Close()
			log.PanicErr(err)
		}
	} else {
		if s.Port == 0 {
			s.Port = 443
		}
		server := http3.Server{
			Addr: fmt.Sprintf(":%d", s.Port),
		}
		gracefulShutdown = func(timeout time.Duration) {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			err := server.Shutdown(ctx)
			if err != nil {
				log.PanicErrF("Failed to shutdown sprout server gracefully", err)
			}
		}

		fmt.Printf("'%s' is started on port: %d\n", s.Name, s.Port)

		err := server.ListenAndServeTLS(s.CertFile, s.KeyFile)
		if err != nil {
			server.Close()
			log.PanicErr(err)
		}
	}

	fmt.Printf("'%s' is shutting down", s.Name)

	// graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	gracefulShutdown(s.ShutdownTimeout)
}

func (s *Server) buildMux() *mux {
	handlers := make(map[epSig]func(*Context))
	for _, ep := range s.endpoints {
		h := func(ctx *Context) {
			err := ep.httpHandler(ctx)
			if err != nil {
				serializer := ctx.Value(ctxKeySerializer).(Serializer)
				err = serializer(err, ctx.Writer)
				if err != nil {
					log.ErrorErrF("Failed to serialize error message", err)
				}
			}
		}

		for _, mth := range ep.methods {
			sig := epSig{
				method:  mth,
				pattern: ep.pattern,
			}
			handlers[sig] = h
		}
	}
	return newMux(handlers)
}

type epSig struct {
	method  string
	pattern string
}

func (s *Server) buildInputEntityValidateFuncs(inputType reflect.Type) ObjectValidateFunc {
	fieldVdFuncs := make([]ValidateFunc, inputType.NumField())
	for i := 0; i < inputType.NumField(); i++ {
		f := inputType.Field(i)
		validateTag := f.Tag.Get("validate")
		validateTag = strings.TrimSpace(validateTag)
		if validateTag != "" {
			for _, v := range s.Validators {
				if vdFunc := v.ValidateFunc(validateTag, i, inputType); vdFunc != nil {
					if fieldVdFuncs[i] == nil {
						fieldVdFuncs[i] = vdFunc
					} else {
						fvdFunc := fieldVdFuncs[i]
						fieldVdFuncs[i] = func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
							err := fvdFunc(ctx, fieldIdx, struValue)
							if err != nil {
								return err
							}
							return vdFunc(ctx, fieldIdx, struValue)
						}
					}
				}
			}
		}

		fieldType := f.Type
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.Struct {
			nestVdFunc := s.buildInputEntityValidateFuncs(fieldType)
			if nestVdFunc != nil {
				if fieldVdFuncs[i] == nil {
					fieldVdFuncs[i] = func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
						return nestVdFunc(ctx, struValue.Field(fieldIdx))
					}
				} else {
					fvdFunc := fieldVdFuncs[i]
					fieldVdFuncs[i] = func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
						err := fvdFunc(ctx, fieldIdx, struValue)
						if err != nil {
							return err
						}
						return nestVdFunc(ctx, struValue.Field(fieldIdx))
					}
				}
			}
		}
	}
	if len(fieldVdFuncs) == 0 {
		return nil
	}
	return func(ctx context.Context, obj reflect.Value) error {
		for obj.Kind() == reflect.Pointer {
			obj = obj.Elem()
		}
		for i := 0; i < obj.NumField(); i++ {
			if fieldVdFuncs[i] != nil {
				err := fieldVdFuncs[i](ctx, i, obj)
				if err != nil {
					return err
				}
			}
		}
		return nil
	}
}

type svrCfg struct {
	Name            string `json:"Name"`
	Port            uint16 `json:"Port"`
	CertFile        string `json:"cert_file"`
	KeyFile         string `json:"key_file"`
	Debug           *bool  `json:"debug"`
	ShutdownTimeout uint64 `json:"shutdown_timeout"`
}
