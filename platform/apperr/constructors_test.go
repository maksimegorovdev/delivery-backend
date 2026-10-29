package apperr_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

func TestConstructors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctor func() *apperr.Error
		want apperr.Code
	}{
		{"invalid argument", apperr.InvalidArgument, apperr.CodeInvalidArgument},
		{"not found", apperr.NotFound, apperr.CodeNotFound},
		{"already exists", apperr.AlreadyExists, apperr.CodeAlreadyExists},
		{"aborted", apperr.Aborted, apperr.CodeAborted},
		{"permission denied", apperr.PermissionDenied, apperr.CodePermissionDenied},
		{"failed precondition", apperr.FailedPrecondition, apperr.CodeFailedPrecondition},
		{"resource exhausted", apperr.ResourceExhausted, apperr.CodeResourceExhausted},
		{"out of range", apperr.OutOfRange, apperr.CodeOutOfRange},
		{"unimplemented", apperr.Unimplemented, apperr.CodeUnimplemented},
		{"internal", apperr.Internal, apperr.CodeInternal},
		{"unavailable", apperr.Unavailable, apperr.CodeUnavailable},
		{"deadline exceeded", apperr.DeadlineExceeded, apperr.CodeDeadlineExceeded},
		{"data loss", apperr.DataLoss, apperr.CodeDataLoss},
		{"unauthenticated", apperr.Unauthenticated, apperr.CodeUnauthenticated},
		{"canceled", apperr.Canceled, apperr.CodeCanceled},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.ctor()

			require.Equal(t, tc.want, err.Code)
			require.Empty(t, err.Reason)
			require.Empty(t, err.Message)
			require.NoError(t, err.Cause)
			require.Empty(t, err.Violations)
		})
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	err := apperr.New(apperr.CodeNotFound)

	require.Equal(t, apperr.CodeNotFound, err.Code)
}

func TestErrorBuilders(t *testing.T) {
	t.Parallel()

	t.Run("with reason and message", func(t *testing.T) {
		t.Parallel()

		err := apperr.NotFound().WithReason("ORDER_NOT_FOUND").WithMessage("order missing")

		require.Equal(t, "ORDER_NOT_FOUND", err.Reason)
		require.Equal(t, "order missing", err.Message)
	})

	t.Run("with violations appends", func(t *testing.T) {
		t.Parallel()

		err := apperr.InvalidArgument().
			WithViolations(apperr.Violation{Field: "a", Message: "bad"}).
			WithViolations(apperr.Violation{Field: "b", Message: "worse"})

		require.Equal(t, []apperr.Violation{
			{Field: "a", Message: "bad"},
			{Field: "b", Message: "worse"},
		}, err.Violations)
	})

	t.Run("wrap sets cause", func(t *testing.T) {
		t.Parallel()

		cause := errors.New("boom")

		err := apperr.Internal().Wrap(cause)

		require.Equal(t, cause, err.Cause)
	})
}

func TestPresetConstructors(t *testing.T) {
	t.Parallel()

	t.Run("invalid request body", func(t *testing.T) {
		t.Parallel()

		err := apperr.InvalidRequestBody()

		require.Equal(t, apperr.CodeInvalidArgument, err.Code)
		require.Equal(t, apperr.ReasonInvalidRequestBody, err.Reason)
		require.Equal(t, apperr.ReasonMessageInvalidRequestBody, err.Message)
	})

	t.Run("invalid credentials", func(t *testing.T) {
		t.Parallel()

		err := apperr.InvalidCredentials()

		require.Equal(t, apperr.CodeUnauthenticated, err.Code)
		require.Equal(t, apperr.ReasonInvalidCredentials, err.Reason)
		require.Equal(t, apperr.ReasonMessageInvalidCredentials, err.Message)
	})

	t.Run("validation failed carries violations", func(t *testing.T) {
		t.Parallel()

		v := apperr.Violation{Field: "email", Message: "must be valid"}

		err := apperr.ValidationFailed(v)

		require.Equal(t, apperr.CodeInvalidArgument, err.Code)
		require.Equal(t, apperr.ReasonValidationFailed, err.Reason)
		require.Equal(t, apperr.ReasonMessageValidationFailed, err.Message)
		require.Equal(t, []apperr.Violation{v}, err.Violations)
	})

	t.Run("validation failed without violations", func(t *testing.T) {
		t.Parallel()

		require.Empty(t, apperr.ValidationFailed().Violations)
	})
}
