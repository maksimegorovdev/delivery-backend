package apperr

import (
	"log/slog"
	"net/http"

	"google.golang.org/grpc/codes"
)

type Code string

const (
	CodeCanceled           Code = "CANCELED"
	CodeUnknown            Code = "UNKNOWN"
	CodeInvalidArgument    Code = "INVALID_ARGUMENT"
	CodeDeadlineExceeded   Code = "DEADLINE_EXCEEDED"
	CodeNotFound           Code = "NOT_FOUND"
	CodeAlreadyExists      Code = "ALREADY_EXISTS"
	CodePermissionDenied   Code = "PERMISSION_DENIED"
	CodeResourceExhausted  Code = "RESOURCE_EXHAUSTED"
	CodeFailedPrecondition Code = "FAILED_PRECONDITION"
	CodeAborted            Code = "ABORTED"
	CodeOutOfRange         Code = "OUT_OF_RANGE"
	CodeUnimplemented      Code = "UNIMPLEMENTED"
	CodeInternal           Code = "INTERNAL"
	CodeUnavailable        Code = "UNAVAILABLE"
	CodeDataLoss           Code = "DATA_LOSS"
	CodeUnauthenticated    Code = "UNAUTHENTICATED"
)

const (
	ReasonInvalidRequestBody = "INVALID_REQUEST_BODY"
	ReasonValidationFailed   = "VALIDATION_FAILED"
	ReasonInvalidCredentials = "INVALID_CREDENTIALS"
)

const (
	MessageInternal           = "internal server error"
	MessageInvalidArgument    = "invalid argument"
	MessageInvalidRequestBody = "invalid request body"
	MessageValidationFailed   = "validation failed"
	MessageNotFound           = "not found"
	MessageAlreadyExists      = "already exists"
	MessageAborted            = "aborted"
	MessageResourceExhausted  = "resource exhausted"
	MessageFailedPrecondition = "failed precondition"
	MessageOutOfRange         = "out of range"
	MessageUnimplemented      = "unimplemented"
	MessageCanceled           = "canceled"
	MessageUnauthenticated    = "unauthenticated"
	MessageInvalidCredentials = "invalid credentials"
	MessagePermissionDenied   = "permission denied"
	MessageDeadlineExceeded   = "deadline exceeded"
	MessageUnavailable        = "unavailable"
)

func (c Code) GRPC() codes.Code {
	switch c {
	case CodeCanceled:
		return codes.Canceled
	case CodeUnknown:
		return codes.Unknown
	case CodeInvalidArgument:
		return codes.InvalidArgument
	case CodeDeadlineExceeded:
		return codes.DeadlineExceeded
	case CodeNotFound:
		return codes.NotFound
	case CodeAlreadyExists:
		return codes.AlreadyExists
	case CodePermissionDenied:
		return codes.PermissionDenied
	case CodeResourceExhausted:
		return codes.ResourceExhausted
	case CodeFailedPrecondition:
		return codes.FailedPrecondition
	case CodeAborted:
		return codes.Aborted
	case CodeOutOfRange:
		return codes.OutOfRange
	case CodeUnimplemented:
		return codes.Unimplemented
	case CodeUnavailable:
		return codes.Unavailable
	case CodeDataLoss:
		return codes.DataLoss
	case CodeUnauthenticated:
		return codes.Unauthenticated
	default:
		return codes.Internal
	}
}

func (c Code) HTTP() int {
	switch c {
	case CodeInvalidArgument, CodeFailedPrecondition, CodeOutOfRange:
		return http.StatusBadRequest
	case CodeUnauthenticated:
		return http.StatusUnauthorized
	case CodePermissionDenied:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeAlreadyExists, CodeAborted:
		return http.StatusConflict
	case CodeResourceExhausted:
		return http.StatusTooManyRequests
	case CodeCanceled:
		return 499
	case CodeUnimplemented:
		return http.StatusNotImplemented
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	case CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

func (c Code) Level() slog.Level {
	switch c {
	case CodeInternal, CodeUnknown, CodeDataLoss, CodeUnavailable, CodeDeadlineExceeded, CodeUnimplemented:
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}

func (c Code) Public() string {
	switch c {
	case CodeInvalidArgument:
		return MessageInvalidArgument
	case CodeNotFound:
		return MessageNotFound
	case CodeAlreadyExists:
		return MessageAlreadyExists
	case CodeAborted:
		return MessageAborted
	case CodeCanceled:
		return MessageCanceled
	case CodeResourceExhausted:
		return MessageResourceExhausted
	case CodeFailedPrecondition:
		return MessageFailedPrecondition
	case CodeOutOfRange:
		return MessageOutOfRange
	case CodeUnimplemented:
		return MessageUnimplemented
	case CodeUnauthenticated:
		return MessageUnauthenticated
	case CodePermissionDenied:
		return MessagePermissionDenied
	case CodeDeadlineExceeded:
		return MessageDeadlineExceeded
	case CodeUnavailable:
		return MessageUnavailable
	default:
		return MessageInternal
	}
}

func CodeFromGRPC(c codes.Code) Code {
	switch c {
	case codes.OK:
		return ""
	case codes.Canceled:
		return CodeCanceled
	case codes.Unknown:
		return CodeUnknown
	case codes.InvalidArgument:
		return CodeInvalidArgument
	case codes.DeadlineExceeded:
		return CodeDeadlineExceeded
	case codes.NotFound:
		return CodeNotFound
	case codes.AlreadyExists:
		return CodeAlreadyExists
	case codes.PermissionDenied:
		return CodePermissionDenied
	case codes.ResourceExhausted:
		return CodeResourceExhausted
	case codes.FailedPrecondition:
		return CodeFailedPrecondition
	case codes.Aborted:
		return CodeAborted
	case codes.OutOfRange:
		return CodeOutOfRange
	case codes.Unimplemented:
		return CodeUnimplemented
	case codes.Internal:
		return CodeInternal
	case codes.Unavailable:
		return CodeUnavailable
	case codes.DataLoss:
		return CodeDataLoss
	case codes.Unauthenticated:
		return CodeUnauthenticated
	default:
		return CodeInternal
	}
}
