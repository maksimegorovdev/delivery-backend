package apperr

func New(code Code) *Error {
	return &Error{Code: code}
}

func (e *Error) Wrap(err error) *Error {
	e.Cause = err
	return e
}

func (e *Error) WithReason(reason string) *Error {
	e.Reason = reason
	return e
}

func (e *Error) WithMessage(msg string) *Error {
	e.Message = msg
	return e
}

func (e *Error) WithViolations(v ...Violation) *Error {
	e.Violations = append(e.Violations, v...)
	return e
}

func InvalidArgument() *Error    { return New(CodeInvalidArgument) }
func NotFound() *Error           { return New(CodeNotFound) }
func AlreadyExists() *Error      { return New(CodeAlreadyExists) }
func Aborted() *Error            { return New(CodeAborted) }
func PermissionDenied() *Error   { return New(CodePermissionDenied) }
func FailedPrecondition() *Error { return New(CodeFailedPrecondition) }
func ResourceExhausted() *Error  { return New(CodeResourceExhausted) }
func OutOfRange() *Error         { return New(CodeOutOfRange) }
func Unimplemented() *Error      { return New(CodeUnimplemented) }
func Internal() *Error           { return New(CodeInternal) }
func Unavailable() *Error        { return New(CodeUnavailable) }
func DeadlineExceeded() *Error   { return New(CodeDeadlineExceeded) }
func DataLoss() *Error           { return New(CodeDataLoss) }
func Unauthenticated() *Error    { return New(CodeUnauthenticated) }
func Canceled() *Error           { return New(CodeCanceled) }

func InvalidRequestBody() *Error {
	return New(CodeInvalidArgument).
		WithReason(ReasonInvalidRequestBody).
		WithMessage(ReasonMessageInvalidRequestBody)
}

func InvalidCredentials() *Error {
	return New(CodeUnauthenticated).
		WithReason(ReasonInvalidCredentials).
		WithMessage(ReasonMessageInvalidCredentials)
}

func ValidationFailed(v ...Violation) *Error {
	return New(CodeInvalidArgument).
		WithReason(ReasonValidationFailed).
		WithMessage(ReasonMessageValidationFailed).
		WithViolations(v...)
}
