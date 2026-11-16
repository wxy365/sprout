package sprout

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// benchMux builds a mux with a few routes, covering the static, the named
// parameter and the regular expression patterns.
func benchMux() *mux {
	handlers := map[epSig]func(*Context){
		{method: http.MethodGet, pattern: "/health"}: func(ctx *Context) {},
		{method: http.MethodGet, pattern: "/users/{id}"}: func(ctx *Context) {},
		{method: http.MethodGet, pattern: "/orders/{id:~\\d+}"}: func(ctx *Context) {},
		{method: http.MethodPost, pattern: "/users"}: func(ctx *Context) {},
	}
	return newMux(handlers)
}

func benchServe(m *mux, method, target string) {
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
}

func BenchmarkMuxServeHTTPStatic(b *testing.B) {
	m := benchMux()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.ServeHTTP(rec, req)
	}
}

// BenchmarkMuxServeHTTPStaticJsonHeaders measures the routing of a request
// that carries the headers a real client sends.
func BenchmarkMuxServeHTTPStaticJsonHeaders(b *testing.B) {
	m := benchMux()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.ServeHTTP(rec, req)
	}
}

func BenchmarkMuxServeHTTPNamedParam(b *testing.B) {
	m := benchMux()
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	rec := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.ServeHTTP(rec, req)
	}
}

func BenchmarkMuxServeHTTPRegexParam(b *testing.B) {
	m := benchMux()
	req := httptest.NewRequest(http.MethodGet, "/orders/12345", nil)
	rec := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.ServeHTTP(rec, req)
	}
}

func BenchmarkMuxServeHTTPNotFound(b *testing.B) {
	m := benchMux()
	req := httptest.NewRequest(http.MethodGet, "/nothing/here", nil)
	rec := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.ServeHTTP(rec, req)
	}
}

// benchInput is the input entity used by the parsing and validating
// benchmarks.
type benchInput struct {
	ID    string `path:"id" validate:"required;[2,32]"`
	Query string `query:"q" validate:"[3,32]"`
	Token string `header:"X-Token" validate:"required"`
	Count int    `query:"count" validate:"[1,100]"`
}

func BenchmarkParseHttpRequest(b *testing.B) {
	req := requestWithPathParams(http.MethodGet, "/users/42?q=hello&count=7", map[string]string{"id": "42"})
	req.Header.Set("X-Token", "secret")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var in benchInput
		if err := parseHttpRequest(&in, req, nil); err != nil {
			b.Fatalf("the request should be parsed, but got: %v", err)
		}
	}
}

func BenchmarkValidateInputValid(b *testing.B) {
	svr := newDefaultServer("bench")
	validateFunc := svr.buildInputEntityValidateFuncs(reflect.TypeOf(benchInput{}))
	in := benchInput{ID: "42", Query: "hello", Token: "secret", Count: 7}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validateFunc(b.Context(), reflect.ValueOf(in)); err != nil {
			b.Fatalf("the input should be valid, but got: %v", err)
		}
	}
}

// BenchmarkValidateInputInvalid measures the path that builds the
// internationalized validation error.
func BenchmarkValidateInputInvalid(b *testing.B) {
	svr := newDefaultServer("bench")
	validateFunc := svr.buildInputEntityValidateFuncs(reflect.TypeOf(benchInput{}))
	in := benchInput{ID: "42", Query: "hello", Count: 7}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validateFunc(b.Context(), reflect.ValueOf(in)); err == nil {
			b.Fatal("the input should be invalid")
		}
	}
}

// benchEndpointOutput is the output entity used by the endpoint benchmark.
type benchEndpointOutput struct {
	Greeting string `json:"greeting"`
}

func BenchmarkEndpointServe(b *testing.B) {
	e := &Endpoint[benchInput, *benchEndpointOutput]{
		Name:    "bench",
		Pattern: "/bench/{id}",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in benchInput) (*benchEndpointOutput, error) {
			return &benchEndpointOutput{Greeting: "hello " + in.ID}, nil
		},
	}
	svr := newDefaultServer("bench")
	e.appendToServer(svr, nil)
	mx := svr.buildMux()

	req := httptest.NewRequest(http.MethodGet, "/bench/42?q=hello&count=7", nil)
	req.Header.Set("X-Token", "secret")
	rec := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mx.ServeHTTP(rec, req)
	}
}
