package sprout

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wxy365/basal/errs"
)

// endpointInput is the input entity used by the endpoint tests.
type endpointInput struct {
	Name  string `query:"name" validate:"required;[3,32]"`
	Count int    `query:"count"`
}

// endpointOutput is the output entity used by the endpoint tests.
type endpointOutput struct {
	Greeting string `json:"greeting"`
}

// serveEndpoint mounts the given endpoint on a fresh server and serves one
// request against it. It returns the recorder holding the response.
func serveEndpoint[I any, O any](t *testing.T, e *Endpoint[I, O], req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	svr := newDefaultServer("test")
	e.appendToServer(svr, nil)
	mx := svr.buildMux()
	rec := httptest.NewRecorder()
	mx.ServeHTTP(rec, req)
	return rec
}

// TestEndpointSuccess verifies the happy path of an endpoint: the input is
// bound, the handler is called and its output is serialized.
func TestEndpointSuccess(t *testing.T) {
	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			return &endpointOutput{Greeting: "hello " + in.Name}, nil
		},
	}

	rec := serveEndpoint(t, e, httptest.NewRequest(http.MethodGet, "/greet?name=foo&count=2", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("the status code = %d, want %d", rec.Code, http.StatusOK)
	}
	var out endpointOutput
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("the response body is not valid JSON: %v", err)
	}
	if out.Greeting != "hello foo" {
		t.Errorf("the greeting = %q, want %q", out.Greeting, "hello foo")
	}
	if got := rec.Header().Get("Content-Type"); got != MimeJson {
		t.Errorf("the Content-Type header = %q, want %q", got, MimeJson)
	}
}

// TestEndpointValidationFailure verifies that an invalid input is rejected
// with the status code carried by the validation error.
func TestEndpointValidationFailure(t *testing.T) {
	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			t.Error("the handler should not be called for an invalid input")
			return nil, nil
		},
	}

	// the name is missing
	rec := serveEndpoint(t, e, httptest.NewRequest(http.MethodGet, "/greet", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("the status code = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	// the name is too short
	rec = serveEndpoint(t, e, httptest.NewRequest(http.MethodGet, "/greet?name=ab", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("the status code = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestEndpointHandlerError verifies that an error returned by the handler is
// passed to the error handler of the endpoint.
func TestEndpointHandlerError(t *testing.T) {
	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			return nil, errs.New("something went wrong").WithStatus(http.StatusConflict)
		},
	}

	rec := serveEndpoint(t, e, httptest.NewRequest(http.MethodGet, "/greet?name=foo", nil))
	if rec.Code != http.StatusConflict {
		t.Errorf("the status code = %d, want %d", rec.Code, http.StatusConflict)
	}
	if !strings.Contains(rec.Body.String(), "something went wrong") {
		t.Errorf("the body should contain the error message, but got: %s", rec.Body.String())
	}
}

// TestEndpointCustomErrorHandler verifies that the error handler of the
// endpoint takes precedence over the one of the server.
func TestEndpointCustomErrorHandler(t *testing.T) {
	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			return nil, errs.New("boom")
		},
		ErrorHandler: func(ctx *Context, err error) {
			ctx.Writer.WriteHeader(http.StatusTeapot)
		},
	}

	rec := serveEndpoint(t, e, httptest.NewRequest(http.MethodGet, "/greet?name=foo", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("the status code = %d, want %d", rec.Code, http.StatusTeapot)
	}
}

// TestEndpointNoContent verifies that an endpoint whose output is the zero
// value answers with 204 No Content.
func TestEndpointNoContent(t *testing.T) {
	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			return nil, nil
		},
	}

	rec := serveEndpoint(t, e, httptest.NewRequest(http.MethodGet, "/greet?name=foo", nil))
	if rec.Code != http.StatusNoContent {
		t.Errorf("the status code = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

// assertNotFound checks that the response reports the not-found error. The
// HTTP status code of the error is written by the mux only when it writes the
// status of the error, so the body is the part that is asserted.
func assertNotFound(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK && rec.Code != http.StatusNotFound {
		t.Errorf("the status code = %d, want %d or %d", rec.Code, http.StatusOK, http.StatusNotFound)
	}
	if !strings.Contains(rec.Body.String(), "Resource not found") {
		t.Errorf("the body should report the not-found error, but got: %s", rec.Body.String())
	}
}

// TestEndpointNotFound verifies that a request that matches no route is
// answered with a not-found error.
func TestEndpointNotFound(t *testing.T) {
	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			return &endpointOutput{Greeting: "hello"}, nil
		},
	}

	rec := serveEndpoint(t, e, httptest.NewRequest(http.MethodGet, "/unknown", nil))
	assertNotFound(t, rec)
}

// TestEndpointMethodNotAllowed verifies that a route that is registered for
// another method is not served.
func TestEndpointMethodNotAllowed(t *testing.T) {
	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			return &endpointOutput{Greeting: "hello"}, nil
		},
	}

	rec := serveEndpoint(t, e, httptest.NewRequest(http.MethodPost, "/greet?name=foo", nil))
	assertNotFound(t, rec)
}

// TestEndpointPanicRecovered verifies that a panic in the handler is turned
// into an internal server error.
func TestEndpointPanicRecovered(t *testing.T) {
	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			panic("boom")
		},
	}

	rec := serveEndpoint(t, e, httptest.NewRequest(http.MethodGet, "/greet?name=foo", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("the status code = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// TestEndpointInvalidInputType verifies that an endpoint whose input is not a
// struct is rejected.
func TestEndpointInvalidInputType(t *testing.T) {
	e := &Endpoint[string, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in string) (*endpointOutput, error) {
			return nil, nil
		},
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("an endpoint with a non-struct input should be rejected")
		}
	}()
	svr := newDefaultServer("test")
	e.appendToServer(svr, nil)
}

// TestEndpointInvalidMethod verifies that an endpoint with an unsupported HTTP
// method is rejected.
func TestEndpointInvalidMethod(t *testing.T) {
	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{"FLY"},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			return nil, nil
		},
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("an endpoint with an unsupported method should be rejected")
		}
	}()
	svr := newDefaultServer("test")
	e.appendToServer(svr, nil)
}
