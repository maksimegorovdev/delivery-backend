package apperr

func New(code Code, msg string) *Error {
	return &Error{
		Code:    code,
		Message: msg,
	}
}

func (e *Error) Wrap(err error) *Error {
	e.Cause = err
	return e
}

func (e *Error) WithReason(reason string) *Error {
	e.Reason = reason
	return e
}

func (e *Error) WithViolations(v ...Violation) *Error {
	e.Violations = append(e.Violations, v...)
	return e
}

func InvalidArgument() *Error {
	return New(CodeInvalidArgument, MessageInvalidArgument)
}

func NotFound() *Error {
	return New(CodeNotFound, MessageNotFound)
}

func AlreadyExists() *Error {
	return New(CodeAlreadyExists, MessageAlreadyExists)
}

func Aborted() *Error {
	return New(CodeAborted, MessageAborted)
}

func PermissionDenied() *Error {
	return New(CodePermissionDenied, MessagePermissionDenied)
}

func FailedPrecondition() *Error {
	return New(CodeFailedPrecondition, MessageFailedPrecondition)
}

func ResourceExhausted() *Error {
	return New(CodeResourceExhausted, MessageResourceExhausted)
}

func OutOfRange() *Error {
	return New(CodeOutOfRange, MessageOutOfRange)
}

func Unimplemented() *Error {
	return New(CodeUnimplemented, MessageUnimplemented)
}

func Internal() *Error {
	return New(CodeInternal, MessageInternal)
}

func Unavailable() *Error {
	return New(CodeUnavailable, MessageUnavailable)
}

func DeadlineExceeded() *Error {
	return New(CodeDeadlineExceeded, MessageDeadlineExceeded)
}

func DataLoss() *Error {
	return New(CodeDataLoss, MessageInternal)
}

func Unauthenticated() *Error {
	return New(CodeUnauthenticated, MessageUnauthenticated)
}

func Canceled() *Error {
	return New(CodeCanceled, MessageCanceled)
}

func InvalidRequestBody() *Error {
	return New(CodeInvalidArgument, MessageInvalidRequestBody).
		WithReason(ReasonInvalidRequestBody)
}

func InvalidCredentials() *Error {
	return New(CodeUnauthenticated, MessageInvalidCredentials).
		WithReason(ReasonInvalidCredentials)
}

func ValidationFailed(v ...Violation) *Error {
	return New(CodeInvalidArgument, MessageValidationFailed).
		WithReason(ReasonValidationFailed).
		WithViolations(v...)
}
