package cli

import (
	"context"
	"crypto/tls"
	"io"
	"mime"
	"net"
	"net/http"
	urlpkg "net/url"
	"reflect"
	"strings"
	"time"

	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/rflt"
	sp "github.com/wxy365/sprout"
	"golang.org/x/net/http2"
)

// Client is an HTTP client wrapper that extends http.Client with convenience
// methods for type-safe API calls using the sprout framework conventions.
type Client struct {
	http.Client
	// header holds the headers that are added to every request.
	header http.Header
}

// clientOptions holds the options of a client.
type clientOptions struct {
	insecureSkipVerify bool
	transport           http.RoundTripper
	header              http.Header
	timeout             time.Duration
}

// ClientOption configures a client. The options are applied to the client in
// the order they are given, after the timeout of the constructor.
type ClientOption func(*clientOptions)

// WithInsecureSkipVerify sets whether the verification of the TLS certificate
// of the server is skipped. It only applies to the transports that use TLS.
func WithInsecureSkipVerify(v bool) ClientOption {
	return func(o *clientOptions) {
		o.insecureSkipVerify = v
	}
}

// WithTransport sets the round tripper of the client. It replaces the
// transport that the protocol specific constructor would use.
func WithTransport(rt http.RoundTripper) ClientOption {
	return func(o *clientOptions) {
		o.transport = rt
	}
}

// WithHeader sets the headers that are added to every request. A header that
// is already carried by a request is not overridden.
func WithHeader(h http.Header) ClientOption {
	return func(o *clientOptions) {
		o.header = h
	}
}

// WithTimeout sets the timeout of the client. It overrides the timeout that
// is passed to the constructor.
func WithTimeout(d time.Duration) ClientOption {
	return func(o *clientOptions) {
		o.timeout = d
	}
}

// newClientOptions builds the options of a client from the given timeout and
// the given options.
func newClientOptions(timeout time.Duration, opts ...ClientOption) *clientOptions {
	o := &clientOptions{timeout: timeout}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(o)
	}
	return o
}

// newClient builds a client with the given options and round tripper.
func newClient(o *clientOptions, transport http.RoundTripper) *Client {
	return &Client{
		Client: http.Client{
			Transport: transport,
			Timeout:   o.timeout,
		},
		header: o.header,
	}
}

// NewH1Client creates a new Client that uses the standard HTTP/1.1 transport.
func NewH1Client(timeout time.Duration, opts ...ClientOption) *Client {
	o := newClientOptions(timeout, opts...)
	transport := o.transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return newClient(o, transport)
}

// NewH2Client creates a new Client that uses the HTTP/2 cleartext (h2c)
// transport, without TLS.
func NewH2Client(timeout time.Duration, opts ...ClientOption) *Client {
	o := newClientOptions(timeout, opts...)
	transport := o.transport
	if transport == nil {
		transport = &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		}
	}
	return newClient(o, transport)
}

// NewH3Client creates a new Client that uses the HTTP/3 (QUIC) transport.
func NewH3Client(timeout time.Duration, opts ...ClientOption) *Client {
	o := newClientOptions(timeout, opts...)
	transport := o.transport
	if transport == nil {
		transport = newH3Transport(o)
	}
	return newClient(o, transport)
}

// NewClient creates a new Client with the specified timeout using standard
// HTTP/1.1 transport.
//
// Deprecated: use NewH1Client instead.
func NewClient(timeout time.Duration) *Client {
	return NewH1Client(timeout)
}

// NewH2cClient creates a new Client that uses HTTP/2 cleartext (h2c) transport
// without TLS.
//
// Deprecated: use NewH2Client instead.
func NewH2cClient(timeout time.Duration) *Client {
	return NewH2Client(timeout)
}

// NewH3ClientWithTLS creates a new Client that uses HTTP/3 (QUIC) transport
// with configurable TLS verification.
//
// Deprecated: use NewH3Client with the WithInsecureSkipVerify option instead.
func NewH3ClientWithTLS(timeout time.Duration, insecureSkipVerify bool) *Client {
	return NewH3Client(timeout, WithInsecureSkipVerify(insecureSkipVerify))
}

// Doer returns a type-safe closure for making API calls. The returned function
// handles serialization of the input struct, request dispatch, deserialization
// of the response, and error mapping.
func Doer[T, R any](client *Client, method, url, contentType string) func(ctx context.Context, in *T) (*R, error) {
	var t T
	var r R
	if reflect.TypeOf(t).Kind() != reflect.Struct {
		panic("The input type should be of struct kind")
	}
	if reflect.TypeOf(r).Kind() != reflect.Struct {
		panic("The output type should be of struct kind")
	}
	return func(ctx context.Context, in *T) (*R, error) {
		out := new(R)
		err := client.Do(ctx, method, url, contentType, in, out)
		return out, err
	}
}

// Do performs an HTTP request with the given method, URL, and content type.
// It serializes the input struct into the request body, dispatches the request,
// and deserializes the response into the output struct.
func (c *Client) Do(ctx context.Context, method, url, contentType string, in, out any) error {
	url, body, header, cookies, err := makeUrlAndBody(url, contentType, in)
	if err != nil {
		return err
	}

	r, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	for k, v := range header {
		r.Header.Set(k, v)
	}
	for _, cookie := range cookies {
		r.AddCookie(&cookie)
	}
	// the headers of the client are only applied when the request does not
	// carry them already
	for k, vs := range c.header {
		if r.Header.Get(k) != "" {
			continue
		}
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	resp, err := c.Client.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if out != nil {
		err = resolveHttpResponse(resp, out)
		if err != nil {
			return err
		}
	}

	return nil
}

// makeUrlAndBody builds the URL, the body, the headers and the cookies of a
// request from the given URL and the given input struct.
func makeUrlAndBody(url, contentType string, in any) (string, io.Reader, map[string]string, []http.Cookie, error) {
	serializer := serializers[contentType]
	if serializer == nil {
		return "", nil, nil, nil, errs.New("Serializer not found for content type: {0}", contentType)
	}
	v := reflect.ValueOf(in).Elem()
	t := reflect.TypeOf(in).Elem()
	header := make(map[string]string)
	var cookies []http.Cookie
	var bodyKeys []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)
		if pname, ok := f.Tag.Lookup("path"); ok {
			valStr, err := rflt.ValueToString(fv)
			if err != nil {
				return "", nil, nil, nil, err
			}
			url = strings.Replace(url, "{"+pname+"}", urlpkg.QueryEscape(valStr), 1)
		} else if pname, ok := f.Tag.Lookup("query"); ok {
			valStr, err := rflt.ValueToString(fv)
			if err != nil {
				return "", nil, nil, nil, err
			}
			if valStr != "" {
				if strings.Contains(url, "?") {
					url = url + "&" + pname + "=" + urlpkg.QueryEscape(valStr)
				} else {
					url = url + "?" + pname + "=" + urlpkg.QueryEscape(valStr)
				}
			}
		} else if pname, ok := f.Tag.Lookup("header"); ok {
			valStr, err := rflt.ValueToString(fv)
			if err != nil {
				return "", nil, nil, nil, err
			}
			if valStr != "" {
				header[pname] = valStr
			}
		} else if pname, ok := f.Tag.Lookup("cookie"); ok {
			valStr, err := rflt.ValueToString(fv)
			if err != nil {
				return "", nil, nil, nil, err
			}
			if valStr != "" {
				cookies = append(cookies, http.Cookie{
					Name:  pname,
					Value: valStr,
				})
			}
		} else {
			bodyKeys = append(bodyKeys, f.Name)
		}
	}
	var body io.Reader
	var err error
	if in != nil {
		body, err = serializer(in, bodyKeys)
		if err != nil {
			return "", nil, nil, nil, err
		}
	}
	return url, body, header, cookies, nil
}

func resolveHttpResponse(resp *http.Response, out any) error {
	respContentType, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		respContentType = sp.MimeJson
	}
	if respContentType == "*/*" {
		respContentType = sp.MimeJson
	}
	deserializer := deserializers[respContentType]
	if deserializer == nil {
		return errs.New("No deserializer found for content type: {0}", respContentType)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		err = deserializer(resp.Body, params, out)
		if err != nil {
			return err
		}
	} else {
		e := new(errs.Err)
		e.WithStatus(resp.StatusCode)
		err = deserializer(resp.Body, params, e)
		if err != nil {
			return err
		}
		return e
	}
	return nil
}
