package rpc

import (
	"errors"
	"net/http"
	"testing"

	"github.com/wxy365/basal/errs"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestToStatusKeepsTheCodeAndTheMessage(t *testing.T) {
	err := errs.New("name is required").WithCode("guest.name.required").WithStatus(http.StatusBadRequest)
	st, ok := status.FromError(toStatus(err))
	if !ok {
		t.Fatalf("the error is not a gRPC status")
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("the code = %s, want %s", st.Code(), codes.InvalidArgument)
	}
	if st.Message() != "name is required" {
		t.Errorf("the message = %q", st.Message())
	}
}

func TestFromStatusRestoresTheBusinessError(t *testing.T) {
	err := errs.New("name is required").WithCode("guest.name.required").WithStatus(http.StatusBadRequest)
	restored := fromStatus(toStatus(err))
	var e *errs.Err
	if !errors.As(restored, &e) {
		t.Fatalf("the restored error is not an *errs.Err: %v", restored)
	}
	if e.Code != "guest.name.required" {
		t.Errorf("the code = %q", e.Code)
	}
	if e.Message != "name is required" {
		t.Errorf("the message = %q", e.Message)
	}
	if e.Status != http.StatusBadRequest {
		t.Errorf("the status = %d", e.Status)
	}
}

func TestStatusMapping(t *testing.T) {
	cases := []struct {
		httpStatus int
		code       codes.Code
	}{
		{http.StatusBadRequest, codes.InvalidArgument},
		{http.StatusUnauthorized, codes.Unauthenticated},
		{http.StatusForbidden, codes.PermissionDenied},
		{http.StatusNotFound, codes.NotFound},
		{http.StatusConflict, codes.Aborted},
		{http.StatusTooManyRequests, codes.ResourceExhausted},
		{http.StatusInternalServerError, codes.Internal},
		{http.StatusServiceUnavailable, codes.Unavailable},
		{http.StatusGatewayTimeout, codes.DeadlineExceeded},
	}
	for _, c := range cases {
		if got := httpStatusToGRPCCode(c.httpStatus); got != c.code {
			t.Errorf("the http %d -> %s, want %s", c.httpStatus, got, c.code)
		}
		if got := grpcCodeToHTTPStatus(c.code); got != c.httpStatus {
			t.Errorf("the %s -> %d, want %d", c.code, got, c.httpStatus)
		}
	}
}

func TestToStatusOnAPlainError(t *testing.T) {
	st, ok := status.FromError(toStatus(errors.New("boom")))
	if !ok {
		t.Fatalf("the error is not a gRPC status")
	}
	if st.Code() != codes.Internal {
		t.Errorf("the code = %s, want %s", st.Code(), codes.Internal)
	}
}

func TestToStatusOnANilError(t *testing.T) {
	if err := toStatus(nil); err != nil {
		t.Errorf("a nil error should stay nil, got %v", err)
	}
}

func TestToStatusKeepsAGRPCStatus(t *testing.T) {
	original := status.Error(codes.NotFound, "gone")
	if got := toStatus(original); got != original {
		t.Errorf("a gRPC status should be returned as it is, got %v", got)
	}
}
