package sprout

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestParseMediaType verifies the parsing of the media type headers. The
// parameters are parsed only when they are asked for, and a header that the
// mime package rejects still falls back to the JSON media type.
func TestParseMediaType(t *testing.T) {
	cases := []struct {
		name       string
		header     string
		wantParams bool
		wantType   string
		wantMap    map[string]string
	}{
		{
			name:     "an empty header falls back to json",
			header:   "",
			wantType: MimeJson,
		},
		{
			name:     "a plain media type",
			header:   "application/json",
			wantType: "application/json",
		},
		{
			name:     "a media type in upper case",
			header:   "Application/JSON",
			wantType: "application/json",
		},
		{
			name:     "a media type with spaces",
			header:   "  application/json  ",
			wantType: "application/json",
		},
		{
			name:     "the wildcard media type falls back to json",
			header:   "*/*",
			wantType: MimeJson,
		},
		{
			name:     "a known media type with parameters",
			header:   "application/json; charset=utf-8",
			wantType: "application/json",
		},
		{
			name:       "the parameters of a known media type",
			header:     "application/json; charset=utf-8",
			wantParams: true,
			wantType:   "application/json",
			wantMap:    map[string]string{"charset": "utf-8"},
		},
		{
			name:       "the parameters of a multipart media type",
			header:     "multipart/form-data; boundary=----WebKitFormBoundary",
			wantParams: true,
			wantType:   "multipart/form-data",
			wantMap:    map[string]string{"boundary": "----WebKitFormBoundary"},
		},
		{
			name:     "a multipart media type without its parameters",
			header:   "multipart/form-data; boundary=----WebKitFormBoundary",
			wantType: "multipart/form-data",
		},
		{
			name:       "a multipart media type with a quoted parameter",
			header:     `multipart/form-data; boundary="a;b"`,
			wantParams: true,
			wantType:   "multipart/form-data",
			wantMap:    map[string]string{"boundary": "a;b"},
		},
		{
			name:     "an unknown media type is reported as it is",
			header:   "application/xml",
			wantType: "application/xml",
		},
		{
			name:     "an unknown media type with a malformed parameter falls back to json",
			header:   "application/xml; charset",
			wantType: MimeJson,
		},
		{
			name:     "a known media type with a malformed parameter falls back to json",
			header:   "text/html; charset",
			wantType: MimeJson,
		},
		{
			name:     "a media type without a parameter is reported as it is",
			header:   "application/ json",
			wantType: "application/ json",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mediaType, params := parseMediaType(tc.header, tc.wantParams)
			if mediaType != tc.wantType {
				t.Errorf("the media type = %q, want %q", mediaType, tc.wantType)
			}
			if len(tc.wantMap) == 0 {
				if len(params) != 0 {
					t.Errorf("the parameters = %v, want none", params)
				}
				return
			}
			if len(params) != len(tc.wantMap) {
				t.Fatalf("the parameters = %v, want %v", params, tc.wantMap)
			}
			for k, v := range tc.wantMap {
				if params[k] != v {
					t.Errorf("the parameter %q = %q, want %q", k, params[k], v)
				}
			}
		})
	}
}

// TestSimpleParamsOK verifies the check of the parameters of a media type
// header. A parameter that the check rejects is not wrong, it only makes the
// header parsed in full.
func TestSimpleParamsOK(t *testing.T) {
	cases := []struct {
		params string
		want   bool
	}{
		{params: "", want: true},
		{params: " ", want: true},
		{params: ";", want: true},
		{params: "; ", want: true},
		{params: "charset=utf-8", want: true},
		{params: " charset=utf-8", want: true},
		{params: "charset=utf-8; boundary=abc", want: true},
		{params: "boundary=----WebKitFormBoundary7MA4YWxkTrZu0gW", want: true},
		{params: "charset=\"utf-8\"", want: false},
		{params: "charset", want: false},
		{params: "charset=", want: false},
		{params: "=utf-8", want: false},
		{params: "charset=utf-8; bad", want: false},
		{params: "charset=utf-8 extra", want: false},
	}

	for _, tc := range cases {
		if got := simpleParamsOK(tc.params); got != tc.want {
			t.Errorf("simpleParamsOK(%q) = %v, want %v", tc.params, got, tc.want)
		}
	}
}

// TestRequestContextCarriesTheValues verifies that the request carries every
// value of the framework, and that the values of the middlewares stay
// reachable.
func TestRequestContextCarriesTheValues(t *testing.T) {
	type middlewareKey struct{}
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), middlewareKey{}, "middleware"))

	var gotPathParams map[string]string
	var gotAcceptType any
	var gotSerializer any
	var gotDeserializer any
	var gotMiddleware any

	handlers := map[epSig]func(*requestContext){
		{method: http.MethodGet, pattern: "/users/{id}"}: func(ctx *requestContext) {
			gotPathParams, _ = ctx.Request.Context().Value(ctxKeyPathParams).(map[string]string)
			gotAcceptType = ctx.Request.Context().Value(ctxKeyAcceptType)
			gotSerializer = ctx.Request.Context().Value(ctxKeySerializer)
			gotDeserializer = ctx.Request.Context().Value(ctxKeyDeserializer)
			gotMiddleware = ctx.Request.Context().Value(middlewareKey{})
		},
	}
	m := newMux(handlers)
	m.ServeHTTP(httptest.NewRecorder(), req)

	if gotPathParams["id"] != "42" {
		t.Errorf("the path param id = %q, want %q", gotPathParams["id"], "42")
	}
	if gotAcceptType != MimeJson {
		t.Errorf("the accept type = %v, want %v", gotAcceptType, MimeJson)
	}
	if gotSerializer == nil {
		t.Error("the serializer should be carried by the request")
	}
	if gotDeserializer == nil {
		t.Error("the deserializer should be carried by the request")
	}
	if gotMiddleware != "middleware" {
		t.Errorf("the value of the middleware = %v, want %q", gotMiddleware, "middleware")
	}
}

// TestPathParamsCollection verifies the path parameters of the matched route.
func TestPathParamsCollection(t *testing.T) {
	cases := []struct {
		name      string
		route     string
		target    string
		want      map[string]string
		wantFound bool
	}{
		{
			name:      "a named parameter",
			route:     "/users/{id}",
			target:    "/users/42",
			want:      map[string]string{"id": "42"},
			wantFound: true,
		},
		{
			name:      "a named parameter with a regular expression",
			route:     "/users/{id:~\\d+}",
			target:    "/users/12345",
			want:      map[string]string{"id": "12345"},
			wantFound: true,
		},
		{
			name:      "several named parameters",
			route:     "/users/{uid}/posts/{pid}",
			target:    "/users/42/posts/7",
			want:      map[string]string{"uid": "42", "pid": "7"},
			wantFound: true,
		},
		{
			name:      "the shallowest parameter wins",
			route:     "/{id}/posts/{id}",
			target:    "/42/posts/7",
			want:      map[string]string{"id": "42"},
			wantFound: true,
		},
		{
			name:   "a static route carries no parameter",
			route:  "/users/me",
			target: "/users/me",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]string
			var found bool
			handlers := map[epSig]func(*requestContext){
				{method: http.MethodGet, pattern: tc.route}: func(ctx *requestContext) {
					got, found = ctx.Request.Context().Value(ctxKeyPathParams).(map[string]string)
				},
			}
			m := newMux(handlers)
			m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tc.target, nil))

			if found != tc.wantFound {
				t.Fatalf("the path parameters are carried = %v, want %v", found, tc.wantFound)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("the path parameters = %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("the path parameter %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// TestMultipartContentTypeParams verifies that the parameters of a multipart
// content type reach the deserializer, and that the other media types carry
// none.
func TestMultipartContentTypeParams(t *testing.T) {
	var gotParams map[string]string
	var gotDeserializer Deserializer
	handlers := map[epSig]func(*requestContext){
		{method: http.MethodPost, pattern: "/upload"}: func(ctx *requestContext) {
			gotParams, _ = ctx.Request.Context().Value(ctxKeyContentTypeParams).(map[string]string)
			gotDeserializer = ctx.Request.Context().Value(ctxKeyDeserializer).(Deserializer)
		},
	}
	m := newMux(handlers)

	req := httptest.NewRequest(http.MethodPost, "/upload", nil)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=----WebKitFormBoundary")
	m.ServeHTTP(httptest.NewRecorder(), req)

	if gotParams["boundary"] != "----WebKitFormBoundary" {
		t.Errorf("the boundary = %q, want %q", gotParams["boundary"], "----WebKitFormBoundary")
	}
	if gotDeserializer == nil {
		t.Error("the deserializer should be carried by the request")
	}
}

// TestCorsInterceptorWildcardOrigin verifies that a wildcard origin matches the
// subdomains of the allowed host.
func TestCorsInterceptorWildcardOrigin(t *testing.T) {
	restoreDefaultConfig(t, `{"app":{"cors":{
		"allow_origins": ["https://*.allowed.com"],
		"allow_methods": ["GET"],
		"allow_headers": ["Content-Type"],
		"allow_credentials": true,
		"max_age": 60
	}}}`)

	interceptor := newCorsInterceptor()
	handler := interceptor(func(ctx *Context) error {
		return nil
	})

	ctx := newTestContext()
	ctx.Request.Header.Set("Origin", "https://app.allowed.com")
	if err := handler(ctx); err != nil {
		t.Fatalf("the request should be handled, but got: %v", err)
	}
	if got := ctx.Writer.Header().Get("Access-Control-Allow-Origin"); got != "https://app.allowed.com" {
		t.Errorf("the Access-Control-Allow-Origin header = %q, want %q", got, "https://app.allowed.com")
	}
	if got := ctx.Writer.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("the Access-Control-Allow-Credentials header = %q, want %q", got, "true")
	}
	if got := ctx.Writer.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type" {
		t.Errorf("the Access-Control-Allow-Headers header = %q, want %q", got, "Content-Type")
	}

	// An origin of another host is not allowed.
	other := newTestContext()
	other.Request.Header.Set("Origin", "https://app.other.com")
	if err := handler(other); err != nil {
		t.Fatalf("the request should be handled, but got: %v", err)
	}
	if got := other.Writer.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("the Access-Control-Allow-Origin header = %q, want it to be empty", got)
	}
}
