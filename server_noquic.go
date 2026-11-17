//go:build noquic

package sprout

import (
	"net/http"
	"os"

	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/log"
)

// startQuic reports that the HTTP/3 (QUIC) support is not available.
//
// This file is only compiled when the "noquic" build tag is set, so that a
// build without QUIC support does not pull in the quic-go dependency.
func (s *httpServer) startQuic(mx http.Handler, quit chan os.Signal) {
	log.PanicErr(errs.New("HTTP/3 support is not compiled in: rebuild without the 'noquic' build tag or drop the cert/key configuration"))
}
