package sprout

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// nestedPointerInner is the nested struct of the nil nested pointer tests. Its
// field carries a validation rule, so that the nested validation is not
// trivial.
type nestedPointerInner struct {
	Code string `validate:"required"`
}

// nestedPointerRequiredInput has a nested pointer field that carries a
// validation rule of its own.
type nestedPointerRequiredInput struct {
	ID    string `validate:"required"`
	Inner *nestedPointerInner `validate:"required"`
}

// nestedPointerInput has a nested pointer field without a validation rule of
// its own: only the fields of the nested struct are validated.
type nestedPointerInput struct {
	ID    string `query:"id" validate:"required"`
	Inner *nestedPointerInner
}

// serveEndpointChain mounts the given endpoint on a fresh server and serves
// one request against it.
func serveEndpointChain[I any](t *testing.T, e *Endpoint[I, *endpointOutput], req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	svr := newDefaultServer("test")
	e.appendToServer(svr, nil)
	mx := svr.buildMux()
	rec := httptest.NewRecorder()
	mx.ServeHTTP(rec, req)
	return rec
}

// TestNestedPointerValidationDoesNotPanic verifies that the validation of a
// nil nested pointer field does not panic: the nested validation is skipped
// for a field that has no value.
func TestNestedPointerValidationDoesNotPanic(t *testing.T) {
	svr := newDefaultServer("test")
	validateFunc := svr.buildInputEntityValidateFuncs(reflect.TypeOf(nestedPointerInput{}))
	if validateFunc == nil {
		t.Fatal("the validation function should have been built")
	}

	in := nestedPointerInput{ID: "1"}
	if err := validateFunc(context.Background(), reflect.ValueOf(in)); err != nil {
		t.Errorf("the nil nested pointer should be skipped, but got: %v", err)
	}
}

// TestEndpointNilNestedPointerIsRejected verifies that an endpoint whose
// nested pointer field is nil and carries a "required" rule is rejected with
// a bad request instead of panicking.
func TestEndpointNilNestedPointerIsRejected(t *testing.T) {
	e := &Endpoint[nestedPointerRequiredInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in nestedPointerRequiredInput) (*endpointOutput, error) {
			t.Error("the handler should not be called for an invalid input")
			return nil, nil
		},
	}

	rec := serveEndpointChain(t, e, httptest.NewRequest(http.MethodGet, "/greet", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("the status code = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestEndpointNilNestedPointerIsNotPanicked verifies that an endpoint whose
// nested pointer field is nil is served normally: the validation of the nested
// struct is skipped, because the nested pointer has no value.
func TestEndpointNilNestedPointerIsNotPanicked(t *testing.T) {
	e := &Endpoint[nestedPointerInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in nestedPointerInput) (*endpointOutput, error) {
			return &endpointOutput{Greeting: "hello " + in.ID}, nil
		},
	}

	rec := serveEndpointChain(t, e, httptest.NewRequest(http.MethodGet, "/greet?id=1", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("the status code = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestEndpointCustomInterceptorRuns verifies that the interceptors of an
// endpoint are applied to the requests it serves.
func TestEndpointCustomInterceptorRuns(t *testing.T) {
	var called bool
	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Interceptors: []Interceptor{
			func(next func(*Context) error) func(ctx *Context) error {
				return func(ctx *Context) error {
					called = true
					return next(ctx)
				}
			},
		},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			return &endpointOutput{Greeting: "hello " + in.Name}, nil
		},
	}

	rec := serveEndpointChain(t, e, httptest.NewRequest(http.MethodGet, "/greet?name=foo", nil))

	if !called {
		t.Error("the interceptor of the endpoint should have been called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("the status code = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestEndpointCustomInterceptorIsRecovered verifies that the built-in panic
// recovery stays the outermost interceptor: a panic raised by the interceptor
// of an endpoint is recovered.
func TestEndpointCustomInterceptorIsRecovered(t *testing.T) {
	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Interceptors: []Interceptor{
			func(next func(*Context) error) func(ctx *Context) error {
				return func(ctx *Context) error {
					panic("boom")
				}
			},
		},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			return &endpointOutput{Greeting: "hello"}, nil
		},
	}

	rec := serveEndpointChain(t, e, httptest.NewRequest(http.MethodGet, "/greet?name=foo", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("the status code = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
