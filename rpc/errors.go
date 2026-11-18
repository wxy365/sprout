package rpc

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/wxy365/basal/errs"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// errorInfoDomain 是承载业务错误码的 ErrorInfo 域。
const errorInfoDomain = "sprout"

// errorInfoStatusKey 是 ErrorInfo 里承载 HTTP 状态码的元数据键。
const errorInfoStatusKey = "status"

// httpStatusToGRPCCode 把 HTTP 状态码映射为 gRPC 状态码。
func httpStatusToGRPCCode(httpStatus int) codes.Code {
	switch httpStatus {
	case http.StatusBadRequest:
		return codes.InvalidArgument
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusForbidden:
		return codes.PermissionDenied
	case http.StatusNotFound:
		return codes.NotFound
	case http.StatusConflict:
		return codes.Aborted
	case http.StatusTooManyRequests:
		return codes.ResourceExhausted
	case 499:
		return codes.Canceled
	case http.StatusServiceUnavailable:
		return codes.Unavailable
	case http.StatusGatewayTimeout:
		return codes.DeadlineExceeded
	case http.StatusInternalServerError:
		return codes.Internal
	}
	return codes.Unknown
}

// grpcCodeToHTTPStatus 把 gRPC 状态码映射回 HTTP 状态码。
func grpcCodeToHTTPStatus(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.NotFound:
		return http.StatusNotFound
	case codes.Aborted:
		return http.StatusConflict
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.Canceled:
		return 499
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.Internal:
		return http.StatusInternalServerError
	}
	return http.StatusInternalServerError
}

// toStatus 把业务错误转换为 gRPC 状态。业务错误码通过 ErrorInfo 承载。
func toStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	var e *errs.Err
	if !errors.As(err, &e) {
		return status.Error(codes.Internal, err.Error())
	}
	st := status.New(httpStatusToGRPCCode(e.Status), e.Message)
	if e.Code == "" {
		return st.Err()
	}
	withDetails, dErr := st.WithDetails(&errdetails.ErrorInfo{
		Reason:   e.Code,
		Domain:   errorInfoDomain,
		Metadata: map[string]string{errorInfoStatusKey: strconv.Itoa(e.Status)},
	})
	if dErr != nil {
		return st.Err()
	}
	return withDetails.Err()
}

// fromStatus 把 gRPC 状态还原为业务错误。
func fromStatus(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	e := &errs.Err{
		Message: st.Message(),
		Status:  grpcCodeToHTTPStatus(st.Code()),
	}
	for _, d := range st.Details() {
		info, ok := d.(*errdetails.ErrorInfo)
		if !ok {
			continue
		}
		e.Code = info.Reason
		if v, ok := info.Metadata[errorInfoStatusKey]; ok {
			if n, convErr := strconv.Atoi(v); convErr == nil {
				e.Status = n
			}
		}
	}
	return e
}
