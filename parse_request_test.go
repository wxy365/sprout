package sprout

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/wxy365/basal/errs"
)

// parseInput is the input entity used by the request parsing tests. It
// exercises the path, query, header, cookie, default and decrypt struct tags.
type parseInput struct {
	ID      string `path:"id"`
	Query   string `query:"q"`
	Token   string `header:"X-Token"`
	Session string `cookie:"sid"`
	Count   int    `query:"count"`
	Ratio   float64
	Active  bool `query:"active"`
}

// defaultInput is the input entity used to verify the "default" struct tag.
type defaultInput struct {
	Plain    string `default:"plain-default"`
	Mixed    string `default:"mixed-default" query:"mixed"`
	Overrid  string `default:"overrid-default" path:"overrid"`
	FromBody string `default:"body-default" json:"from_body"`
}

// requestWithPathParams builds a request whose context carries the path
// parameters that the router collects.
func requestWithPathParams(method, target string, params map[string]string) *http.Request {
	req := newTestRequest(method, target, "")
	ctx := context.WithValue(req.Context(), ctxKeyPathParams, params)
	return req.WithContext(ctx)
}

func TestParseHttpRequestBindings(t *testing.T) {
	req := requestWithPathParams(http.MethodGet, "/users/42?q=hello", map[string]string{"id": "42"})
	req.Header.Set("X-Token", "secret")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sess-1"})

	var in parseInput
	if err := parseHttpRequest(&in, req, nil); err != nil {
		t.Fatalf("the request should be parsed, but got: %v", err)
	}

	if in.ID != "42" {
		t.Errorf("the path param ID = %q, want %q", in.ID, "42")
	}
	if in.Query != "hello" {
		t.Errorf("the query param Query = %q, want %q", in.Query, "hello")
	}
	if in.Token != "secret" {
		t.Errorf("the header param Token = %q, want %q", in.Token, "secret")
	}
	if in.Session != "sess-1" {
		t.Errorf("the cookie param Session = %q, want %q", in.Session, "sess-1")
	}
}

func TestParseHttpRequestNumericBindings(t *testing.T) {
	req := newTestRequest(http.MethodGet, "/x?count=7&active=true", "")

	var in parseInput
	if err := parseHttpRequest(&in, req, nil); err != nil {
		t.Fatalf("the request should be parsed, but got: %v", err)
	}
	if in.Count != 7 {
		t.Errorf("the query param Count = %d, want %d", in.Count, 7)
	}
	if !in.Active {
		t.Error("the query param Active should be true")
	}
}

// TestParseHttpRequestInvalidValue verifies that a value that cannot be
// converted into the type of the field is reported as an error.
func TestParseHttpRequestInvalidValue(t *testing.T) {
	req := newTestRequest(http.MethodGet, "/x?count=not-a-number", "")

	var in parseInput
	err := parseHttpRequest(&in, req, nil)
	if err == nil {
		t.Fatal("the invalid value should be rejected")
	}
	if !strings.Contains(err.Error(), "not-a-number") {
		t.Errorf("the error should mention the offending value, but got: %v", err)
	}
}

// TestParseHttpRequestDefault verifies the "default" struct tag.
func TestParseHttpRequestDefault(t *testing.T) {
	req := requestWithPathParams(http.MethodGet, "/x/overridden", map[string]string{"overrid": "from-path"})

	var in defaultInput
	if err := parseHttpRequest(&in, req, nil); err != nil {
		t.Fatalf("the request should be parsed, but got: %v", err)
	}

	if in.Plain != "plain-default" {
		t.Errorf("the default value Plain = %q, want %q", in.Plain, "plain-default")
	}
	if in.Overrid != "from-path" {
		t.Errorf("the path param should win over the default value, but Overrid = %q", in.Overrid)
	}
	// The current binding order is: path > default > query > header > cookie.
	// A field carrying both a "default" and a "query" tag therefore always
	// receives the default value and the query parameter is ignored.
	if in.Mixed != "mixed-default" {
		t.Errorf("the default value Mixed = %q, want %q", in.Mixed, "mixed-default")
	}
}

// TestParseHttpRequestBody verifies that the request body is deserialized and
// that the body wins over the default value of the field.
func TestParseHttpRequestBody(t *testing.T) {
	req := newTestRequest(http.MethodPost, "/x", `{"from_body":"from-body"}`)
	req.Header.Set("Content-Type", MimeJson)

	var in defaultInput
	if err := parseHttpRequest(&in, req, nil); err != nil {
		t.Fatalf("the request should be parsed, but got: %v", err)
	}
	if in.FromBody != "from-body" {
		t.Errorf("the body value FromBody = %q, want %q", in.FromBody, "from-body")
	}
}

// TestParseHttpRequestBodyKeepsDefault verifies that a field that is absent
// from the body keeps the value assigned by the "default" tag.
func TestParseHttpRequestBodyKeepsDefault(t *testing.T) {
	req := newTestRequest(http.MethodPost, "/x", `{"other":"value"}`)
	req.Header.Set("Content-Type", MimeJson)

	var in defaultInput
	if err := parseHttpRequest(&in, req, nil); err != nil {
		t.Fatalf("the request should be parsed, but got: %v", err)
	}
	if in.FromBody != "body-default" {
		t.Errorf("the default value should be kept, but FromBody = %q", in.FromBody)
	}
}

// TestParseHttpRequestDecrypt verifies that the "decrypt" struct tag decrypts
// the bound value with the registered decrypter.
func TestParseHttpRequestDecrypt(t *testing.T) {
	type secretInput struct {
		Secret string `header:"X-Secret" decrypt:"test"`
	}

	decrypters := map[string]func(cipher []byte) ([]byte, error){
		"test": func(cipher []byte) ([]byte, error) {
			return []byte("decrypted:" + string(cipher)), nil
		},
	}

	req := newTestRequest(http.MethodGet, "/x", "")
	req.Header.Set("X-Secret", "cipher")

	var in secretInput
	if err := parseHttpRequest(&in, req, decrypters); err != nil {
		t.Fatalf("the request should be parsed, but got: %v", err)
	}
	if in.Secret != "decrypted:cipher" {
		t.Errorf("the decrypted value Secret = %q, want %q", in.Secret, "decrypted:cipher")
	}
}

// TestParseHttpRequestDecryptFailure verifies that a failing decrypter leaves
// the original value untouched instead of failing the whole request.
func TestParseHttpRequestDecryptFailure(t *testing.T) {
	type secretInput struct {
		Secret string `header:"X-Secret" decrypt:"test"`
	}

	decrypters := map[string]func(cipher []byte) ([]byte, error){
		"test": func(cipher []byte) ([]byte, error) {
			return nil, errs.New("cannot decrypt")
		},
	}

	req := newTestRequest(http.MethodGet, "/x", "")
	req.Header.Set("X-Secret", "cipher")

	var in secretInput
	if err := parseHttpRequest(&in, req, decrypters); err != nil {
		t.Fatalf("a failing decrypter should not fail the request, but got: %v", err)
	}
	if in.Secret != "cipher" {
		t.Errorf("the raw value should be kept, but Secret = %q", in.Secret)
	}
}

// TestParseHttpRequestDecryptUnknownAlgorithm verifies that an unregistered
// decrypter leaves the value untouched.
func TestParseHttpRequestDecryptUnknownAlgorithm(t *testing.T) {
	type secretInput struct {
		Secret string `header:"X-Secret" decrypt:"unknown"`
	}

	req := newTestRequest(http.MethodGet, "/x", "")
	req.Header.Set("X-Secret", "cipher")

	var in secretInput
	if err := parseHttpRequest(&in, req, nil); err != nil {
		t.Fatalf("the request should be parsed, but got: %v", err)
	}
	if in.Secret != "cipher" {
		t.Errorf("the raw value should be kept, but Secret = %q", in.Secret)
	}
}
