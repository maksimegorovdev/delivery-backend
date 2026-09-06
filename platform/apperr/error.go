package apperr

import (
	"context"
	"errors"
	"log/slog"
)

type Error struct {
	Code       Code
	Reason     string
	Message    string
	Cause      error
	Violations []Violation
}

type Violation struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.Code.Public()
	}
	if e.Cause != nil {
		msg += ": " + e.Cause.Error()
	}
	return msg
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func (e *Error) Is(target error) bool {
	var t *Error
	ok := errors.As(target, &t)
	return ok && t.Code == e.Code
}

func (e *Error) Public() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code.Public()
}

func (e *Error) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("code", e.Code.String()),
		slog.String("public_message", e.Public()),
	}
	if e.Reason != "" {
		attrs = append(attrs, slog.String("reason", e.Reason))
	}
	if e.Cause != nil {
		attrs = append(attrs, slog.String("cause", e.Cause.Error()))
	}
	if len(e.Violations) > 0 {
		attrs = append(attrs, slog.Any("violations", e.Violations))
	}
	return slog.GroupValue(attrs...)
}

var (
	ErrCanceled           = &Error{Code: CodeCanceled}
	ErrUnknown            = &Error{Code: CodeUnknown}
	ErrInvalidArgument    = &Error{Code: CodeInvalidArgument}
	ErrDeadlineExceeded   = &Error{Code: CodeDeadlineExceeded}
	ErrNotFound           = &Error{Code: CodeNotFound}
	ErrAlreadyExists      = &Error{Code: CodeAlreadyExists}
	ErrPermissionDenied   = &Error{Code: CodePermissionDenied}
	ErrResourceExhausted  = &Error{Code: CodeResourceExhausted}
	ErrFailedPrecondition = &Error{Code: CodeFailedPrecondition}
	ErrAborted            = &Error{Code: CodeAborted}
	ErrOutOfRange         = &Error{Code: CodeOutOfRange}
	ErrUnimplemented      = &Error{Code: CodeUnimplemented}
	ErrInternal           = &Error{Code: CodeInternal}
	ErrUnavailable        = &Error{Code: CodeUnavailable}
	ErrDataLoss           = &Error{Code: CodeDataLoss}
	ErrUnauthenticated    = &Error{Code: CodeUnauthenticated}
)

func As(err error) (*Error, bool) {
	if e, ok := errors.AsType[*Error](err); ok {
		return e, true
	}
	return nil, false
}

func From(err error) *Error {
	if err == nil {
		return nil
	}
	if e, ok := As(err); ok {
		return e
	}
	switch {
	case errors.Is(err, context.Canceled):
		return Canceled().Wrap(err)
	case errors.Is(err, context.DeadlineExceeded):
		return DeadlineExceeded().Wrap(err)
	}
	return Internal().Wrap(err)
}
