package sprout

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/quic-go/quic-go/http3"
	"github.com/wxy365/basal/ds/slices"
	"github.com/wxy365/basal/env"
	"github.com/wxy365/basal/lei"
	"github.com/wxy365/basal/rflt"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"
)

type Server struct {
	started bool
	name    string
	port    int32
	mux     *mux
}

func (s *Server) Name() string {
	return s.name
}

func (s *Server) Start() {
	if s.started {
		return
	}
	certCfgJson, err := env.GetStr("TLS_CERT_CFG", "")
	if err != nil {
		panic(err)
	}
	var gracefulShutdownHook func(timeout time.Duration)
	if certCfgJson == "" {
		server := &http.Server{
			Addr:    fmt.Sprintf(":%d", s.port),
			Handler: h2c.NewHandler(s.mux, &http2.Server{}),
		}
		err = server.ListenAndServe()
		if err != nil {
			server.Close()
			panic(err)
		}
		gracefulShutdownHook = func(timeout time.Duration) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = server.Shutdown(ctx)
			if err != nil {
				lei.PanicErrF("Failed to shutdown sprout server gracefully", err)
			}
		}
	} else {
		certCfg := make(map[string]string)
		err = json.Unmarshal([]byte(certCfgJson), &certCfg)
		if err != nil {
			return
		}
		certFile := certCfg["cert_file"]
		if certFile == "" {
			panic("The tls certification file path is not specified")
		}
		keyFile := certCfg["key_file"]
		if keyFile == "" {
			panic("The tls key file path is not specified")
		}
		if s.port == 0 {
			s.port = 443
		}
		server := http3.Server{
			Addr: fmt.Sprintf(":%d", s.port),
		}
		err = server.ListenAndServeTLS(certFile, keyFile)
		if err != nil {
			server.Close()
			panic(err)
		}
		gracefulShutdownHook = func(timeout time.Duration) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = server.Shutdown(ctx)
			if err != nil {
				lei.PanicErrF("Failed to shutdown sprout server gracefully", err)
			}
		}
	}
	s.started = true

	// graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	gracefulShutdownHook(time.Second * 10)
}

// signature of endpoint: http method and path pattern contained
type epSig struct {
	method  string
	pattern string
}

func buildHandler(endpoints []Endpoint) map[epSig]func(writer http.ResponseWriter, request *http.Request) {
	handlers := make(map[epSig]func(writer http.ResponseWriter, request *http.Request))
	for _, ep := range endpoints {
		validateEp(ep)

		epHandler := func(writer http.ResponseWriter, request *http.Request) error {
			in := ep.InputEntity
			serializer := request.Context().Value(ctxKeySerializer).(Serializer)
			responseContentType := request.Context().Value(ctxKeyAcceptType).(string)
			deserializer := request.Context().Value(ctxKeyDeserializer).(Deserializer)

			if in != nil {
				// parse body
				err := deserializer(request, in)
				if err != nil {
					return err
				}

				// parse header、query、cookie
				iv := reflect.ValueOf(in).Elem()
				for i := 0; i < iv.NumField(); i++ {
					fv := iv.Field(i)
					tag := iv.Type().Field(i).Tag
					var valStr *string
					if def, ok := tag.Lookup("default"); ok && fv.IsZero() {
						valStr = &def
					}

					var queryMap map[string]string
					if key, ok := tag.Lookup("path"); ok {
						pathParams := request.Context().Value(ctxKeyPathParams)
						if pathParams != nil {
							if val, exists := pathParams.(map[string]string)[key]; exists {
								valStr = &val
							}
						}
					}
					if valStr == nil {
						if key, ok := tag.Lookup("query"); ok {
							if len(queryMap) == 0 {
								queryMap = make(map[string]string)
								for k, v := range request.URL.Query() {
									queryMap[k] = v[0]
								}
							}
							if val, exists := queryMap[key]; exists {
								valStr = &val
							}
						}
						if valStr == nil {
							if key, ok := tag.Lookup("header"); ok {
								val := request.Header.Get(key)
								if val != "" {
									valStr = &val
								}
							}
							if valStr == nil {
								if key, ok := tag.Lookup("cookie"); ok {
									cookie, err := request.Cookie(key)
									if err == nil {
										val := cookie.String()
										valStr = &val
									}
								}
							}
						}
					}
					if valStr != nil {
						err = rflt.UnmarshalValue(fv, *valStr)
						if err != nil {
							return err
						}
					}
				}
			}

			serviceLogEnabled, _ := env.GetBool("SERVICE_LOG_ENABLED", false)
			if serviceLogEnabled {
				lei.Debug(`Endpoint "{0}" input: "{1}"`, ep.Name, in)
			}

			out, err := ep.Handler(request.Context(), in)
			if err != nil {
				r := request.WithContext(context.WithValue(request.Context(), ctxKeyEndpointError, err))
				*request = *r
				return err
			}

			if out != nil {
				if serviceLogEnabled {
					lei.Debug(`Endpoint "{0}", output: "{1}"`, ep.Name, out)
				}
				writer.Header().Set("Content-Type", responseContentType)
				err = serializer(out, writer)
				if err != nil {
					return err
				}
			}
			return nil
		}

		var ics []Interceptor
		ics = append(ics, newCircuitBreakerInterceptor(ep.Name), newRateLimiterInterceptor(ep.Name)) // append circuit breaker, rate limiter
		ics = append(ics, ep.Interceptors...)

		for i := len(ics); i > 0; i-- {
			ic := ics[i-1]
			epHandler = ic(epHandler)
		}

		h := func(w http.ResponseWriter, r *http.Request) {
			err := epHandler(w, r)
			if err != nil {
				serializer := r.Context().Value(ctxKeySerializer).(Serializer)
				err = serializer(err, w)
				if err != nil {
					lei.ErrorErrF("Failed to serialize error message", err)
				}
			}
		}

		for _, mth := range ep.Methods {
			sig := epSig{
				method:  mth,
				pattern: ep.Pattern,
			}
			handlers[sig] = h
		}
	}
	return handlers
}

func validateEp(ep Endpoint) {
	if reflect.TypeOf(ep.InputEntity).Kind() != reflect.Pointer {
		panic(fmt.Sprintf("The input entity of endpoint '%s' must be pointer kind", ep.Name))
	}
	allowedMethods := []string{
		http.MethodConnect, http.MethodGet, http.MethodHead,
		http.MethodPut, http.MethodPost, http.MethodPatch,
		http.MethodOptions, http.MethodTrace, http.MethodDelete,
	}
	for _, mth := range ep.Methods {
		if slices.Lookup(allowedMethods, mth, func(left, right string) bool {
			return left == right
		}) < 0 {
			panic("Invalid http method: " + mth)
		}
	}
}

type ServerBuilder struct {
	server    *Server
	done      bool
	name      string
	port      int32
	endpoints []Endpoint
}

func NewServerBuilder() *ServerBuilder {
	return &ServerBuilder{}
}

func (s *ServerBuilder) Build() *Server {
	if !s.done {
		s.server = &Server{
			name: s.name,
			port: s.port,
		}
		handlers := buildHandler(s.endpoints)
		s.server.mux = newMux(handlers)
		s.done = true
	}
	return s.server
}

func (s *ServerBuilder) Name(name string) *ServerBuilder {
	s.name = name
	return s
}

func (s *ServerBuilder) Port(port int32) *ServerBuilder {
	s.port = port
	return s
}

func (s *ServerBuilder) AddEndpoint(e ...Endpoint) *ServerBuilder {
	s.endpoints = append(s.endpoints, e...)
	return s
}
