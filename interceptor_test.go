package sprout

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/cfg/def"
)

// newTestContext builds a request context that carries the serializer and the
// deserializer of the framework.
func newTestContext() *Context {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), ctxKeySerializer, Serializer(SerializeJson))
	ctx = context.WithValue(ctx, ctxKeyDeserializer, Deserializer(DeserializeJson))
	ctx = context.WithValue(ctx, ctxKeyAcceptType, MimeJson)
	return &Context{Request: req.WithContext(ctx), Writer: httptest.NewRecorder()}
}

// TestRecoverInterceptor verifies that a panic in the handler is turned into an
// internal server error instead of crashing the server.
func TestRecoverInterceptor(t *testing.T) {
	handler := recoverInterceptor(func(ctx *Context) error {
		panic("boom")
	})

	ctx := newTestContext()
	if err := handler(ctx); err != nil {
		t.Errorf("the panic should be recovered, but got: %v", err)
	}
	if ctx.Writer.(*httptest.ResponseRecorder).Code != http.StatusInternalServerError {
		t.Errorf("the status code = %d, want %d",
			ctx.Writer.(*httptest.ResponseRecorder).Code, http.StatusInternalServerError)
	}
}

// TestRecoverInterceptorPassesThrough verifies that a normal call is not
// altered by the recover interceptor.
func TestRecoverInterceptorPassesThrough(t *testing.T) {
	want := errs.New("some error")
	handler := recoverInterceptor(func(ctx *Context) error {
		return want
	})

	if err := handler(newTestContext()); err != want {
		t.Errorf("the error = %v, want %v", err, want)
	}
}

// TestCircuitBreakerInterceptorTrips verifies that the circuit breaker opens
// after enough consecutive server errors and that the subsequent requests are
// rejected.
func TestCircuitBreakerInterceptorTrips(t *testing.T) {
	interceptor := newCircuitBreakerInterceptor("test-breaker")
	handler := interceptor(func(ctx *Context) error {
		// The circuit breaker reads the endpoint error from the context to
		// decide whether the request was successful.
		err := errs.New("internal error").WithStatus(http.StatusInternalServerError)
		newRequest := ctx.Request.WithContext(context.WithValue(ctx, ctxKeyEndpointError, err))
		*ctx.Request = *newRequest
		return err
	})

	// The default settings trip the breaker after 10 consecutive failures.
	for i := 0; i < 10; i++ {
		if err := handler(newTestContext()); err == nil {
			t.Fatalf("the request %d should have failed", i)
		}
	}

	err := handler(newTestContext())
	if err != ErrCircuitBroken {
		t.Errorf("the error = %v, want %v", err, ErrCircuitBroken)
	}
}

// TestCircuitBreakerInterceptorKeepsClientErrors verifies that a client error
// does not count as a failure of the endpoint.
func TestCircuitBreakerInterceptorKeepsClientErrors(t *testing.T) {
	interceptor := newCircuitBreakerInterceptor("test-breaker-client-error")
	handler := interceptor(func(ctx *Context) error {
		err := errs.New("bad request").WithStatus(http.StatusBadRequest)
		newRequest := ctx.Request.WithContext(context.WithValue(ctx, ctxKeyEndpointError, err))
		*ctx.Request = *newRequest
		return err
	})

	for i := 0; i < 20; i++ {
		if err := handler(newTestContext()); err == nil {
			t.Fatalf("the request %d should have failed", i)
		}
	}

	// The breaker must still be closed, so the request is not rejected with
	// ErrCircuitBroken.
	err := handler(newTestContext())
	if err == ErrCircuitBroken {
		t.Error("the client errors should not trip the circuit breaker")
	}
}

// TestRateLimiterInterceptorRejects verifies that the rate limiter rejects the
// requests that exceed the configured rate.
func TestRateLimiterInterceptorRejects(t *testing.T) {
	// Configure a limiter that allows a single request per second.
	restoreDefaultConfig(t, `{"app":{"limiters":{"test-limiter":{
		"token_rate": 1,
		"token_bucket_size": 1,
		"client_identifier_type": "IP",
		"client_token_rate": 1,
		"client_token_bucket_size": 1
	}}}}`)

	interceptor := newRateLimiterInterceptor("test-limiter")
	handler := interceptor(func(ctx *Context) error {
		return nil
	})

	allowed := 0
	rejected := 0
	for i := 0; i < 50; i++ {
		err := handler(newTestContext())
		if err == nil {
			allowed++
			continue
		}
		if err == ErrRateLimited {
			rejected++
			continue
		}
		t.Fatalf("the unexpected error: %v", err)
	}

	if allowed == 0 {
		t.Error("the limiter should allow at least one request")
	}
	if rejected == 0 {
		t.Error("the limiter should reject the requests that exceed the rate")
	}
}

// TestRateLimiterInterceptorAllows verifies that the requests under the
// configured rate are not rejected.
func TestRateLimiterInterceptorAllows(t *testing.T) {
	restoreDefaultConfig(t, `{"app":{"limiters":{"test-limiter-allow":{
		"token_rate": 1000,
		"token_bucket_size": 1000
	}}}}`)

	interceptor := newRateLimiterInterceptor("test-limiter-allow")
	handler := interceptor(func(ctx *Context) error {
		return nil
	})

	for i := 0; i < 100; i++ {
		if err := handler(newTestContext()); err != nil {
			t.Fatalf("the request %d should be allowed, but got: %v", i, err)
		}
	}
}

// TestRateLimiterInterceptorWithoutClientRate verifies that a limiter that
// does not configure a client token rate does not limit the clients. A token
// bucket of zero size never grants a token, so a client limiter that is built
// without a token rate would reject every request.
func TestRateLimiterInterceptorWithoutClientRate(t *testing.T) {
	restoreDefaultConfig(t, `{"app":{"limiters":{"test-limiter-no-client-rate":{
		"token_rate": 1000,
		"token_bucket_size": 1000
	}}}}`)

	interceptor := newRateLimiterInterceptor("test-limiter-no-client-rate")
	handler := interceptor(func(ctx *Context) error {
		return nil
	})

	for i := 0; i < 100; i++ {
		if err := handler(newTestContext()); err != nil {
			t.Fatalf("the request %d should be allowed, but got: %v", i, err)
		}
	}
}

// TestRateLimiterInterceptorWithoutBucketSize verifies that a limiter that
// does not configure a token bucket size still grants the tokens of its rate.
// A token bucket of zero size never grants a token, which would reject every
// request.
func TestRateLimiterInterceptorWithoutBucketSize(t *testing.T) {
	restoreDefaultConfig(t, `{"app":{"limiters":{"test-limiter-no-bucket":{
		"token_rate": 100
	}}}}`)

	interceptor := newRateLimiterInterceptor("test-limiter-no-bucket")
	handler := interceptor(func(ctx *Context) error {
		return nil
	})

	allowed := 0
	for i := 0; i < 100; i++ {
		if err := handler(newTestContext()); err == nil {
			allowed++
			continue
		} else if err != ErrRateLimited {
			t.Fatalf("the unexpected error: %v", err)
		}
	}
	if allowed == 0 {
		t.Error("the limiter should grant the tokens of its rate")
	}
}

// TestRateLimiterInterceptorUsesClientIdentifier verifies that the client
// limiter is applied per client identity.
func TestRateLimiterInterceptorUsesClientIdentifier(t *testing.T) {
	restoreDefaultConfig(t, `{"app":{"limiters":{"test-client-limiter":{
		"token_rate": 1000,
		"token_bucket_size": 1000,
		"client_identifier_type": "TestID",
		"client_token_rate": 1,
		"client_token_bucket_size": 1
	}}}}`)

	RegisterClientIdentifier("TestID", func(r *http.Request) string {
		return r.Header.Get("X-Client")
	})

	interceptor := newRateLimiterInterceptor("test-client-limiter")
	handler := interceptor(func(ctx *Context) error {
		return nil
	})

	// The first request of the client is allowed, the second one is rejected.
	first := newTestContext()
	first.Request.Header.Set("X-Client", "client-1")
	if err := handler(first); err != nil {
		t.Fatalf("the first request should be allowed, but got: %v", err)
	}
	second := newTestContext()
	second.Request.Header.Set("X-Client", "client-1")
	if err := handler(second); err != ErrRateLimited {
		t.Errorf("the error = %v, want %v", err, ErrRateLimited)
	}

	// Another client has its own token bucket.
	other := newTestContext()
	other.Request.Header.Set("X-Client", "client-2")
	if err := handler(other); err != nil {
		t.Errorf("the request of another client should be allowed, but got: %v", err)
	}
}

// TestCorsInterceptorWithoutSettings verifies that no CORS header is set when
// the CORS is not configured.
func TestCorsInterceptorWithoutSettings(t *testing.T) {
	restoreDefaultConfig(t, `{}`)

	interceptor := newCorsInterceptor()
	if interceptor == nil {
		t.Fatal("the CORS interceptor should not be nil")
	}
	handler := interceptor(func(ctx *Context) error {
		return nil
	})

	ctx := newTestContext()
	ctx.Request.Header.Set("Origin", "https://example.com")
	if err := handler(ctx); err != nil {
		t.Fatalf("the request should be handled, but got: %v", err)
	}
	if got := ctx.Writer.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("the Access-Control-Allow-Origin header = %q, want it to be empty", got)
	}
}

// TestCorsInterceptorAllowsOrigin verifies that an allowed origin receives the
// CORS headers.
func TestCorsInterceptorAllowsOrigin(t *testing.T) {
	restoreDefaultConfig(t, `{"app":{"cors":{
		"allow_origins": ["https://allowed.com"],
		"allow_methods": ["GET","POST"],
		"allow_headers": ["Content-Type"],
		"max_age": 600
	}}}`)

	interceptor := newCorsInterceptor()
	handler := interceptor(func(ctx *Context) error {
		return nil
	})

	ctx := newTestContext()
	ctx.Request.Header.Set("Origin", "https://allowed.com")
	if err := handler(ctx); err != nil {
		t.Fatalf("the request should be handled, but got: %v", err)
	}
	if got := ctx.Writer.Header().Get("Access-Control-Allow-Origin"); got != "https://allowed.com" {
		t.Errorf("the Access-Control-Allow-Origin header = %q, want %q", got, "https://allowed.com")
	}
	if got := ctx.Writer.Header().Get("Access-Control-Allow-Methods"); got != "GET,POST" {
		t.Errorf("the Access-Control-Allow-Methods header = %q, want %q", got, "GET,POST")
	}
	if got := ctx.Writer.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("the Access-Control-Max-Age header = %q, want %q", got, "600")
	}
}

// restoreDefaultConfig replaces the default configuration for the duration of
// the test and restores the previous one afterwards.
func restoreDefaultConfig(t *testing.T, jsonStr string) {
	t.Helper()
	previous := def.GetCfg()
	if _, err := def.SetDefaultFromStr(jsonStr); err != nil {
		t.Fatalf("the configuration should be loaded, but got: %v", err)
	}
	t.Cleanup(func() {
		def.SetDefault(previous)
	})
}
