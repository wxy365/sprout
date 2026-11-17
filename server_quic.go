//go:build !noquic

package sprout

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/quic-go/quic-go/http3"
	"github.com/wxy365/basal/log"
)

// startQuic starts an HTTP/3 (QUIC) server with TLS.
//
// This file is only compiled when the "noquic" build tag is not set, so that
// a build without QUIC support does not pull in the quic-go dependency.
func (s *httpServer) startQuic(mx http.Handler, quit chan os.Signal) {
	if s.Port == 0 {
		s.Port = 443
	}
	server := http3.Server{
		Addr:    fmt.Sprintf(":%d", s.Port),
		Handler: mx,
	}

	go func() {
		<-quit
		fmt.Printf("'%s' is shutting down\n", s.Name)
		ctx, cancel := context.WithTimeout(context.Background(), s.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.PanicErrF("Failed to shutdown server gracefully", err)
		}
	}()

	fmt.Printf("'%s' is started on port: %d\n", s.Name, s.Port)
	err := server.ListenAndServeTLS(s.CertFile, s.KeyFile)
	if err != nil && err != http.ErrServerClosed {
		log.PanicErr(err)
	}
}
