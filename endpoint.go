package sprout

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/wxy365/basal/ds/slices"
	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/log"
)

type Handler[I any, O any] func(ctx *Context, in I) (O, error)

type ErrorHandler func(ctx *Context, err error)

var defaultErrHandler = func(ctx *Context, err error) {
	var leiErr *errs.Err
	statusCode := http.StatusInternalServerError
	if errors.As(err, &leiErr) {
		if leiErr.Status > 0 {
			statusCode = leiErr.Status
		}
	}
	ctx.Writer.WriteHeader(statusCode)
	ctx.Writer.Header().Set("Content-Type", MimeJson)
	ctx.Writer.Write([]byte(leiErr.Error()))
}

type Endpoint[I any, O any] struct {
	Name         string
	Pattern      string
	Methods      []string
	Handler      Handler[I, O]
	Interceptors []Interceptor
	// error handler for this endpoint, if not set (normally, you don’t need to set it),
	// the error handler registered on the server will be used
	ErrorHandler
}

func (e *Endpoint[I, O]) appendToServer(svr *Server, decrypters map[string]func(cipher []byte) ([]byte, error)) {
	// validate endpoint methods
	allowedMethods := []string{
		http.MethodConnect, http.MethodGet, http.MethodHead,
		http.MethodPut, http.MethodPost, http.MethodPatch,
		http.MethodOptions, http.MethodTrace, http.MethodDelete,
	}
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

	httpHandler := func(ctx *Context) error {
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
			inputStr, _ := json.Marshal(in)
			log.Debug(`Endpoint [{0}] input: {1}`, r.name, inputStr)
		}

		out, err := e.Handler(ctx, in)
		if err != nil {
			if svr.Debug {
				log.ErrorErrF(`Endpoint [{0}] failed to process the request`, err, r.name)
			}
			newRequest := ctx.Request.WithContext(context.WithValue(ctx, ctxKeyEndpointError, err))
			*ctx.Request = *newRequest
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

	ics := []Interceptor{recoverInterceptor}
	circuitBreaker := newCircuitBreakerInterceptor(r.name)
	if circuitBreaker != nil {
		ics = append(ics, circuitBreaker)
	}
	rateLimiter := newRateLimiterInterceptor(r.name)
	if rateLimiter != nil {
		ics = append(ics, rateLimiter)
	}
	corsInterceptor := newCorsInterceptor()
	if corsInterceptor != nil {
		ics = append(ics, corsInterceptor)
	}
	for i := len(ics); i > 0; i-- {
		ic := ics[i-1]
		httpHandler = ic(httpHandler)
	}
	errHandler := e.ErrorHandler
	if errHandler == nil {
		errHandler = svr.ErrorHandler
	}
	if errHandler == nil {
		errHandler = defaultErrHandler
	}
	r.httpHandler = func(ctx *Context) error {
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
	httpHandler func(ctx *Context) error
}
