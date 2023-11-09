package svr

import (
	"fmt"
	coll "gitee.com/wxy25/common/collection"
	"gitee.com/wxy25/common/env"
	"gitee.com/wxy25/common/lei"
	"gitee.com/wxy25/common/rflt"
	"github.com/quic-go/quic-go/http3"
	"net/http"
	"reflect"
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
	certFile, err := env.GetStr("HTTPS_CERT_FILE", "")
	if err != nil {
		panic(err)
	}
	if certFile == "" {
		panic("The certification file path for https server must be specified by environment variable: HTTPS_CERT_FILE")
	}
	keyFile, err := env.GetStr("HTTPS_KEY_FILE", "")
	if err != nil {
		panic(err)
	}
	if keyFile == "" {
		panic("The key file path for https server must be specified by environment variable: HTTPS_KEY_FILE")
	}
	if s.port == 0 {
		s.port = 443
	}
	err = http3.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", s.port), certFile, keyFile, s.mux)
	if err != nil {
		panic(err)
	}
	s.started = true
}

// signature of endpoint: http method and path pattern contained
type epSig struct {
	method  string
	pattern string
}

func buildHandler(endpoints []Endpoint, interceptors []InterceptorDef) map[epSig]func(writer http.ResponseWriter, request *http.Request) {
	handlers := make(map[epSig]func(writer http.ResponseWriter, request *http.Request))
	icmap := make(map[string]Interceptor)
	for _, ic := range interceptors {
		icmap[ic.Name()] = ic.Interceptor()
	}
	for _, ep := range endpoints {
		validateEp(ep)

		epHandler := func(writer http.ResponseWriter, request *http.Request) error {
			in := ep.InputEntity()
			out := ep.OutputEntity()
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
						err = rflt.PopulateValueFromString(fv, *valStr)
						if err != nil {
							return err
						}
					}
				}
			}

			serviceLogEnabled, _ := env.GetBool("SERVICE_LOG_ENABLED", false)
			if serviceLogEnabled {
				lei.Debug(`Endpoint "{0}" input: "{1}"`, ep.Name(), fmt.Sprintf("%+v", in))
			}

			err := ep.Handler()(request.Context(), in, out)
			if err != nil {
				return err
			}

			if out != nil {
				if serviceLogEnabled {
					lei.Debug(`Endpoint "{0}" output: "{1}"`, ep.Name(), fmt.Sprintf("%+v", out))
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
		for _, icname := range ep.Interceptors() {
			ics = append(ics, icmap[icname])
		}

		for i := len(ics); i > 0; i-- {
			ic := ics[i-1]
			epHandler = ic(epHandler)
		}

		h := func(w http.ResponseWriter, r *http.Request) {
			err := epHandler(w, r)
			if err != nil {
				lei.ErrorErr(err)
				serializer := r.Context().Value(ctxKeySerializer).(Serializer)
				err = serializer(err, w)
				if err != nil {
					lei.ErrorErrF("Failed to serialize error message", err)
				}
			}
		}

		for _, mth := range ep.Methods() {
			sig := epSig{
				method:  mth,
				pattern: ep.Pattern(),
			}
			handlers[sig] = h
		}
	}
	return handlers
}

func validateEp(ep Endpoint) {
	if reflect.TypeOf(ep.InputEntity()).Kind() != reflect.Pointer {
		panic(fmt.Sprintf("The input entity of endpoint '%s' must be pointer kind", ep.Name()))
	}
	if reflect.TypeOf(ep.OutputEntity()).Kind() != reflect.Pointer {
		panic(fmt.Sprintf("The output entity of endpoint '%s' must be pointer kind", ep.Name()))
	}
	for _, mth := range ep.Methods() {
		if coll.LookupInSlice([]string{
			http.MethodConnect, http.MethodGet, http.MethodHead,
			http.MethodPut, http.MethodPost, http.MethodPatch,
			http.MethodOptions, http.MethodTrace, http.MethodDelete,
		}, mth) < 0 {
			panic("Invalid http method: " + mth)
		}
	}
}

type ServerBuilder struct {
	server       *Server
	done         bool
	name         string
	port         int32
	endpoints    []Endpoint
	interceptors []InterceptorDef
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
		handlers := buildHandler(s.endpoints, s.interceptors)
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

func (s *ServerBuilder) AddInterceptor(i ...InterceptorDef) *ServerBuilder {
	s.interceptors = append(s.interceptors, i...)
	return s
}
