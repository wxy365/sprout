package sprout

import (
	"errors"
	"net/http"
	"reflect"

	"github.com/wxy365/basal/ds/slices"
	"github.com/wxy365/basal/errs"
	bjson "github.com/wxy365/basal/json"
	"github.com/wxy365/basal/log"
)

// handlerFunc is the function signature for business logic handlers.
// It receives a request context and a deserialized input struct, and returns
// an output struct or an error.
//
// The exported alias Handler is declared in api.go, which collects the public
// API surface of the package.
type handlerFunc[I any, O any] func(ctx *requestContext, in I) (O, error)

// errorHandlerFunc is the function signature for handling errors that occur during
// request processing. It is invoked by the endpoint or server when a handler
// returns an error.
//
// The exported alias ErrorHandler is declared in api.go, which collects the
// public API surface of the package.
type errorHandlerFunc func(ctx *requestContext, err error)

var defaultErrHandler = func(ctx *requestContext, err error) {
	var leiErr *errs.Err
	statusCode := http.StatusInternalServerError
	if errors.As(err, &leiErr) {
		if leiErr.Status > 0 {
			statusCode = leiErr.Status
		}
	}
	ctx.Writer.Header().Set("Content-Type", MimeJson)
	ctx.Writer.WriteHeader(statusCode)
	if leiErr != nil {
		ctx.Writer.Write([]byte(leiErr.Error()))
	} else {
		ctx.Writer.Write([]byte(err.Error()))
	}
}

// endpoint defines a typed API endpoint with its pattern, HTTP methods,
// business logic handler, interceptors, and error handler.
//
// The exported alias Endpoint is declared in api.go, which collects the public
// API surface of the package.
type endpoint[I any, O any] struct {
	// Name is the endpoint name, used for logging and circuit breaker identification.
	Name string
	// Pattern is the URI path pattern, supporting static segments, named parameters
	// (e.g. {id}), regex parameters (e.g. {name:~re}), format specifiers (%s, %d),
	// and wildcards (*).
	Pattern string
	// Methods is the list of HTTP methods this endpoint responds to (e.g. GET, POST).
	Methods []string
	// Handler is the business logic function invoked when the endpoint matches a request.
	Handler handlerFunc[I, O]
	// Interceptors is the list of interceptors applied to this endpoint. Built-in
	// interceptors (panic recovery, circuit breaker, rate limiter, CORS) are added
	// automatically during registration.
	Interceptors []interceptorFunc
	// ErrorHandler is the error handler for this endpoint. If nil, the server-level
	// error handler is used as a fallback.
	ErrorHandler
}

func (e *endpoint[I, O]) appendToServer(svr *httpServer, decrypters map[string]func(cipher []byte) ([]byte, error)) {
	// validate endpoint methods
	for _, mth := range e.Methods {
		if slices.Lookup(allowedMethods, mth, func(left, right string) bool {
			return left == right
		}) < 0 {
			panic("Invalid http method: " + mth)
		}
	}

	// validate endpoint input type
	var input I
	inputType := reflect.TypeOf(input)
	for inputType.Kind() == reflect.Pointer {
		inputType = inputType.Elem()
	}
	if inputType.Kind() != reflect.Struct {
		log.Panic("Endpoint ({0}) input type must be a struct, but got: [{1}]", e.Name, inputType)
	}

	r := &refinedEndpoint{
		name:    e.Name,
		pattern: e.Pattern,
		methods: e.Methods,
	}

	validateFunc := svr.buildInputEntityValidateFuncs(inputType)

	httpHandler := func(ctx *requestContext) error {
		var in I
		err := parseHttpRequest(&in, ctx.Request, decrypters)
		if err != nil {
			return errs.Wrap(err, "Failed to parse request of endpoint [{0}]", e.Name).WithStatus(http.StatusBadRequest)
		}
		err = validateFunc(ctx, reflect.ValueOf(in))
		if err != nil {
			return err
		}

		if svr.Debug {
			inputStr, _ := bjson.Marshal(in)
			log.Debug(`Endpoint [{0}] input: {1}`, r.name, string(inputStr))
		}

		out, err := e.Handler(ctx, in)
		if err != nil {
			if svr.Debug {
				log.ErrorErrF(`Endpoint [{0}] failed to process the request`, err, r.name)
			}
			ctx.setEndpointError(err)
			return err
		}

		if !reflect.ValueOf(out).IsZero() {
			if svr.Debug {
				log.Debug(`Endpoint [{0}], output: {1}`, r.name, out)
			}
			responseContentType := ctx.Value(ctxKeyAcceptType).(string)
			ctx.Writer.Header().Set("Content-Type", responseContentType)
			serializer := ctx.Value(ctxKeySerializer).(Serializer)
			err = serializer(out, ctx.Writer)
			if err != nil {
				return err
			}
		} else {
			ctx.Writer.WriteHeader(http.StatusNoContent)
		}
		return nil
	}

	ics := []interceptorFunc{recoverInterceptor}
	circuitBreaker := newCircuitBreakerInterceptor(r.name)
	ics = append(ics, circuitBreaker)
	rateLimiter := newRateLimiterInterceptor(r.name)
	ics = append(ics, rateLimiter)
	corsInterceptor := newCorsInterceptor()
	if corsInterceptor != nil {
		ics = append(ics, corsInterceptor)
	}
	// The interceptors of the endpoint are applied after the built-in ones, so
	// that the built-in panic recovery stays the outermost interceptor.
	ics = append(ics, e.Interceptors...)
	httpHandler = appendServerInterceptors(httpHandler, ics...)
	errHandler := e.ErrorHandler
	if errHandler == nil {
		errHandler = svr.ErrorHandler
	}
	if errHandler == nil {
		errHandler = defaultErrHandler
	}
	r.httpHandler = func(ctx *requestContext) error {
		err := httpHandler(ctx)
		if err != nil {
			errHandler(ctx, err)
		}
		return nil
	}

	svr.endpoints = append(svr.endpoints, r)
}

type refinedEndpoint struct {
	name        string
	pattern     string
	methods     []string
	httpHandler func(ctx *requestContext) error
}
