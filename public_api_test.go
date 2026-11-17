package sprout_test

import (
	"net/http"
	"testing"

	"github.com/wxy365/sprout"
)

// The tests of this file live in the external test package, so that they only
// rely on the public API of the framework. They check that the exported names
// keep their meaning and their signature, whatever happens to the internal
// implementation.

type publicApiCtx struct {
	Name string `map:"app.name"`
}

type publicApiReq struct {
	ID string `path:"id" json:"id"`
}

type publicApiResp struct {
	ID string `json:"id"`
}

// publicApiHandler is a business logic function written against the public
// API only.
func publicApiHandler(ctx *sprout.Context, in publicApiReq) (*publicApiResp, error) {
	return &publicApiResp{ID: in.ID}, nil
}

// TestPublicApiNames checks that the exported names of the framework are the
// ones the users rely on, and that they keep their signature.
func TestPublicApiNames(t *testing.T) {
	// the types of the public API
	var (
		_ *sprout.App[*publicApiCtx]
		_ *sprout.Endpoint[publicApiReq, *publicApiResp]
		_ *sprout.Server
		_ *sprout.Context
		_ sprout.Handler[publicApiReq, *publicApiResp]
		_ sprout.ErrorHandler
		_ sprout.Interceptor
		_ sprout.Validator
		_ sprout.ValidateFunc
		_ sprout.ObjectValidateFunc
		_ sprout.ClientIdentifier
		_ sprout.Serializer
		_ sprout.Deserializer
		_ *sprout.RequiredValidator
		_ *sprout.RequiredByValidator
		_ *sprout.EitherValidator
		_ *sprout.NumberRangeValidator
		_ *sprout.StringLengthValidator
		_ *sprout.EmailValidator
	)
	// the content types and the error values
	_ = sprout.MimeJson
	_ = sprout.MimeMultipartForm
	_ = sprout.MimeUrlencodedForm
	_ = sprout.MimeText
	_ = sprout.MimeHtml
	_ = sprout.MimePdf
	_ = sprout.ErrRateLimited
	_ = sprout.ErrCircuitBroken
}

// TestPublicApiMount checks that an endpoint can be declared and mounted
// through the public API only.
func TestPublicApiMount(t *testing.T) {
	app := &sprout.App[*publicApiCtx]{Name: "public-api-test"}

	endpoint := &sprout.Endpoint[publicApiReq, *publicApiResp]{
		Name:    "PublicApi",
		Pattern: "/items/{id}",
		Methods: []string{http.MethodGet},
		Handler: publicApiHandler,
	}
	sprout.Mount(endpoint, app)

	if len(app.Initializers) != 1 {
		t.Fatalf("the endpoint has not been mounted: %d initializer(s)", len(app.Initializers))
	}
}
