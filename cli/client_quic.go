//go:build !noquic

package cli

import (
	"crypto/tls"
	"net/http"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// newH3Transport creates the HTTP/3 (QUIC) transport of the client.
//
// This file is only compiled when the "noquic" build tag is not set, so that
// a build without QUIC support does not pull in the quic-go dependency.
func newH3Transport(o *clientOptions) http.RoundTripper {
	return &http3.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: o.insecureSkipVerify,
		},
		DisableCompression: false,
		QUICConfig: &quic.Config{
			KeepAlivePeriod: time.Second * 15,
			EnableDatagrams: true,
		},
	}
}
