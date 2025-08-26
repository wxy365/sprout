package cli

import (
	"context"
	"crypto/tls"
	"io"
	"mime"
	"net"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/rflt"
	sp "github.com/wxy365/sprout"
	"golang.org/x/net/http2"
)

type Client struct {
	http.Client
}

func NewClient(timeout time.Duration) *Client {
	return &Client{
		http.Client{
			Timeout: timeout,
		},
	}
}

func NewH3Client(timeout time.Duration) *Client {
	roundTripper := &http3.RoundTripper{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		DisableCompression: false,
		QUICConfig: &quic.Config{
			KeepAlivePeriod: time.Second * 15,
			EnableDatagrams: true,
		},
	}
	client := http.Client{
		Transport: roundTripper,
		Timeout:   timeout,
	}
	return &Client{Client: client}
}

func NewH2cClient(timeout time.Duration) *Client {
	client := http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
		Timeout: timeout,
	}
	return &Client{Client: client}
}

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

func (c *Client) Do(ctx context.Context, method, url, contentType string, in, out any) error {
	url, body, err := makeUrlAndBody(url, contentType, in)
	if err != nil {
		return err
	}

	r, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	resp, err := c.Client.Do(r)
	if err != nil {
		return err
	}

	if out != nil {
		err = resolveHttpResponse(resp, out)
		if err != nil {
			return err
		}
	}

	return nil
}

func makeUrlAndBody(url, contentType string, in any) (string, io.Reader, error) {
	serializer := serializers[contentType]
	if serializer == nil {
		return "", nil, errs.New("Serializer not found for content type: {0}", contentType)
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
				return "", nil, err
			}
			url = strings.Replace(url, "{"+pname+"}", valStr, 1)
		} else if pname, ok := f.Tag.Lookup("query"); ok {
			valStr, err := rflt.ValueToString(fv)
			if err != nil {
				return "", nil, err
			}
			if valStr != "" {
				if strings.Contains(url, "?") {
					url = url + "&" + pname + "=" + valStr
				} else {
					url = url + "?" + pname + "=" + valStr
				}
			}
		} else if pname, ok := f.Tag.Lookup("header"); ok {
			valStr, err := rflt.ValueToString(fv)
			if err != nil {
				return "", nil, err
			}
			if valStr != "" {
				header[pname] = valStr
			}
		} else if pname, ok := f.Tag.Lookup("cookie"); ok {
			valStr, err := rflt.ValueToString(fv)
			if err != nil {
				return "", nil, err
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
			return "", nil, err
		}
	}
	return url, body, nil
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
