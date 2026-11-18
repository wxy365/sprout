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

	"github.com/wxy365/basal/log"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// httpServer represents an HTTP server instance managed by the application.
// It holds network configuration, validator chains, error handlers, and the
// collection of registered endpoints.
//
// The exported alias Server is declared in api.go, which collects the public
// API surface of the package.
type httpServer struct {
	// Name is the server name, used for configuration matching and logging.
	Name string
	// Port is the TCP port the server listens on. Defaults to 80 for HTTP and
	// 443 for HTTPS/QUIC when not explicitly set.
	Port uint16
	// CertFile is the path to the TLS certificate file. When set alongside
	// KeyFile, the server uses HTTP/3 (QUIC) with TLS; otherwise HTTP/2 h2c.
	CertFile string
	// KeyFile is the path to the TLS private key file.
	KeyFile string
	// Debug enables debug-level request logging for endpoints on this server.
	Debug bool
	// ShutdownTimeout is the maximum duration to wait for in-flight requests
	// to complete during graceful shutdown.
	ShutdownTimeout time.Duration

	// Validators is the ordered list of input validators applied to endpoint
	// request parameters.
	Validators []validator
	// ErrorHandler is the fallback error handler used by endpoints that do not
	// define their own. If nil, a default handler is used.
	ErrorHandler errorHandlerFunc

	endpoints []*refinedEndpoint
}

func newDefaultServer(name string) *httpServer {
	return &httpServer{
		Name:            name,
		Debug:           false,
		ShutdownTimeout: 10 * time.Second,
		Validators:      defaultValidators(),
		ErrorHandler:    defaultErrHandler,
	}
}

func (s *httpServer) start() {
	mx := s.buildMux()

	// set up signal handling before starting the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	if s.CertFile == "" || s.KeyFile == "" {
		s.startH2c(mx, quit)
	} else {
		// the HTTP/3 (QUIC) server lives in a build-tagged file, so that a
		// build without QUIC support does not pull in the quic-go dependency
		s.startQuic(mx, quit)
	}
}

// startH2c starts an HTTP/2 cleartext (h2c) server.
func (s *httpServer) startH2c(mx http.Handler, quit chan os.Signal) {
	if s.Port == 0 {
		s.Port = 80
	}
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", s.Port),
		Handler:      h2c.NewHandler(mx, &http2.Server{}),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		<-quit
		fmt.Printf("'%s' is shutting down\n", s.Name)
		ctx, cancel := context.WithTimeout(context.Background(), s.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.PanicErrF("Failed to shutdown server gracefully", err)
		}
	}()

	fmt.Printf("'%s' is started on port: %d\n", s.Name, s.Port)
	err := server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		log.PanicErr(err)
	}
}

func (s *httpServer) buildMux() *mux {
	handlers := make(map[epSig]func(*requestContext))
	for _, ep := range s.endpoints {
		h := func(ctx *requestContext) {
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

// buildInputEntityValidateFuncs 按给定的校验器列表构建入参类型的校验函数。
// 它不依赖具体的服务器，因此可以被其它传输方式（例如 gRPC）复用。
func buildInputEntityValidateFuncs(validators []validator, inputType reflect.Type) objectValidateFunc {
	fieldVdFuncs := make([]validateFunc, inputType.NumField())
	for i := 0; i < inputType.NumField(); i++ {
		f := inputType.Field(i)
		validateTag := f.Tag.Get("validate")
		validateTag = strings.TrimSpace(validateTag)
		if validateTag != "" {
			for _, v := range validators {
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
			nestVdFunc := buildInputEntityValidateFuncs(validators, fieldType)
			if nestVdFunc != nil {
				// The nested field may be a nil pointer at request time: in that
				// case there is nothing to validate, so the nested validation is
				// skipped instead of dereferencing the nil pointer.
				nestedField := func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
					fieldValue, ok := dereferenceValue(struValue.Field(fieldIdx))
					if !ok {
						return nil
					}
					return nestVdFunc(ctx, fieldValue)
				}
				if fieldVdFuncs[i] == nil {
					fieldVdFuncs[i] = nestedField
				} else {
					fvdFunc := fieldVdFuncs[i]
					fieldVdFuncs[i] = func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
						err := fvdFunc(ctx, fieldIdx, struValue)
						if err != nil {
							return err
						}
						return nestedField(ctx, fieldIdx, struValue)
					}
				}
			}
		}
	}
	if len(fieldVdFuncs) == 0 {
		return nil
	}
	return func(ctx context.Context, obj reflect.Value) error {
		obj, ok := dereferenceValue(obj)
		if !ok {
			return nil
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

// buildInputEntityValidateFuncs 构建该服务器上入参类型的校验函数。
func (s *httpServer) buildInputEntityValidateFuncs(inputType reflect.Type) objectValidateFunc {
	return buildInputEntityValidateFuncs(s.Validators, inputType)
}

// dereferenceValue dereferences the pointer values of the given value. It
// returns false when the value is invalid or when it is a nil pointer, in
// which case there is no value to work with.
func dereferenceValue(value reflect.Value) (reflect.Value, bool) {
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return value, false
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return value, false
	}
	return value, true
}

type svrCfg struct {
	Name            string `map:"name"`
	Port            uint16 `map:"port"`
	CertFile        string `map:"cert_file"`
	KeyFile         string `map:"key_file"`
	Debug           *bool  `map:"debug"`
	ShutdownTimeout uint64 `map:"shutdown_timeout"` // in milliseconds
}
