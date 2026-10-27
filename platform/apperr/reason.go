package apperr

//nolint:gosec // это коды причин ошибок, а не учётные данные
const (
	ReasonInvalidRequestBody = "INVALID_REQUEST_BODY"
	ReasonValidationFailed   = "VALIDATION_FAILED"
	ReasonInvalidCredentials = "INVALID_CREDENTIALS"
)

//nolint:gosec // это тексты ошибок, а не учётные данные
const (
	ReasonMessageInvalidRequestBody = "invalid request body"
	ReasonMessageValidationFailed   = "validation failed"
	ReasonMessageInvalidCredentials = "invalid credentials"
)
