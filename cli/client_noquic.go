//go:build noquic

package cli

import (
	"net/http"

	"github.com/wxy365/basal/errs"
)

// newH3Transport returns the transport of the HTTP/3 (QUIC) clients that are
// built without the QUIC support.
//
// This file is only compiled when the "noquic" build tag is set, so that a
// build without QUIC support does not pull in the quic-go dependency.
func newH3Transport(o *clientOptions) http.RoundTripper {
	return unsupportedH3Transport{}
}

// unsupportedH3Transport is the round tripper of the HTTP/3 (QUIC) clients
// that are built without the QUIC support.
type unsupportedH3Transport struct{}

// RoundTrip always fails, because the HTTP/3 support is not compiled in.
func (unsupportedH3Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, errs.New("HTTP/3 support is not compiled in: rebuild without the 'noquic' build tag to use the HTTP/3 client")
}
