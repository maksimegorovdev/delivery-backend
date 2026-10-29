package grpcerr_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/apperr/grpcerr"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/interceptors"
)

// allCodes — все коды apperr; функция вместо глобальной переменной (gochecknoglobals).
func allCodes() []apperr.Code {
	return []apperr.Code{
		apperr.CodeCanceled,
		apperr.CodeUnknown,
		apperr.CodeInvalidArgument,
		apperr.CodeDeadlineExceeded,
		apperr.CodeNotFound,
		apperr.CodeAlreadyExists,
		apperr.CodePermissionDenied,
		apperr.CodeResourceExhausted,
		apperr.CodeFailedPrecondition,
		apperr.CodeAborted,
		apperr.CodeOutOfRange,
		apperr.CodeUnimplemented,
		apperr.CodeInternal,
		apperr.CodeUnavailable,
		apperr.CodeDataLoss,
		apperr.CodeUnauthenticated,
	}
}

// serverSide пропускает ошибку через серверный интерцептор, как это делает gRPC-сервер сервиса.
func serverSide(t *testing.T, handlerErr error) error {
	t.Helper()

	_, err := interceptors.Error()(
		t.Context(),
		nil,
		&grpc.UnaryServerInfo{},
		func(context.Context, any) (any, error) { return nil, handlerErr },
	)
	require.Error(t, err)

	return err
}

func mustApperr(t *testing.T, err error) *apperr.Error {
	t.Helper()

	e, ok := apperr.As(err)
	require.True(t, ok, "expected *apperr.Error in chain, got %T", err)

	return e
}

func TestMap(t *testing.T) {
	t.Parallel()

	t.Run("nil stays nil", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, grpcerr.Map(nil))
	})

	t.Run("non-grpc error becomes internal and keeps cause", func(t *testing.T) {
		t.Parallel()

		cause := errors.New("connection refused")

		got := mustApperr(t, grpcerr.Map(cause))

		require.Equal(t, apperr.CodeInternal, got.Code)
		require.ErrorIs(t, got, cause)
		require.Equal(t, apperr.MessageInternal, got.Public())
	})

	t.Run("status without details maps by grpc code", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			code codes.Code
			want apperr.Code
		}{
			{"canceled", codes.Canceled, apperr.CodeCanceled},
			{"unknown", codes.Unknown, apperr.CodeUnknown},
			{"invalid argument", codes.InvalidArgument, apperr.CodeInvalidArgument},
			{"deadline exceeded", codes.DeadlineExceeded, apperr.CodeDeadlineExceeded},
			{"not found", codes.NotFound, apperr.CodeNotFound},
			{"already exists", codes.AlreadyExists, apperr.CodeAlreadyExists},
			{"permission denied", codes.PermissionDenied, apperr.CodePermissionDenied},
			{"resource exhausted", codes.ResourceExhausted, apperr.CodeResourceExhausted},
			{"failed precondition", codes.FailedPrecondition, apperr.CodeFailedPrecondition},
			{"aborted", codes.Aborted, apperr.CodeAborted},
			{"out of range", codes.OutOfRange, apperr.CodeOutOfRange},
			{"unimplemented", codes.Unimplemented, apperr.CodeUnimplemented},
			{"internal", codes.Internal, apperr.CodeInternal},
			{"unavailable", codes.Unavailable, apperr.CodeUnavailable},
			{"data loss", codes.DataLoss, apperr.CodeDataLoss},
			{"unauthenticated", codes.Unauthenticated, apperr.CodeUnauthenticated},
			{"unknown grpc code falls back to internal", codes.Code(99), apperr.CodeInternal},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				src := status.Error(tc.code, "peer message")

				got := mustApperr(t, grpcerr.Map(src))

				require.Equal(t, tc.want, got.Code)
				require.Empty(t, got.Reason)
				require.Empty(t, got.Violations)
				require.ErrorIs(t, got, src)
			})
		}
	})

	t.Run("foreign domain error info is ignored", func(t *testing.T) {
		t.Parallel()

		st, err := status.New(codes.NotFound, "peer message").WithDetails(&errdetails.ErrorInfo{
			Domain:   "other.service",
			Reason:   "CUSTOM",
			Metadata: map[string]string{"reason": "OTHER_REASON"},
		})
		require.NoError(t, err)

		got := mustApperr(t, grpcerr.Map(st.Err()))

		require.Equal(t, apperr.CodeNotFound, got.Code)
		require.Empty(t, got.Reason)
		require.Empty(t, got.Message)
	})

	t.Run("bad request details become violations without error info", func(t *testing.T) {
		t.Parallel()

		st, err := status.New(codes.InvalidArgument, "bad").WithDetails(&errdetails.BadRequest{
			FieldViolations: []*errdetails.BadRequest_FieldViolation{
				{Field: "email", Description: "must be valid"},
				{Field: "name", Description: "cannot be blank"},
			},
		})
		require.NoError(t, err)

		got := mustApperr(t, grpcerr.Map(st.Err()))

		require.Equal(t, apperr.CodeInvalidArgument, got.Code)
		require.Equal(t, []apperr.Violation{
			{Field: "email", Message: "must be valid"},
			{Field: "name", Message: "cannot be blank"},
		}, got.Violations)
	})
}

func TestMap_RoundTripThroughInterceptor(t *testing.T) {
	t.Parallel()

	t.Run("every apperr code survives the round trip", func(t *testing.T) {
		t.Parallel()

		for _, code := range allCodes() {
			t.Run(code.String(), func(t *testing.T) {
				t.Parallel()

				sent := apperr.New(code)

				wire := serverSide(t, sent)
				require.Equal(t, code.GRPC(), status.Code(wire))

				got := mustApperr(t, grpcerr.Map(wire))

				require.Equal(t, code, got.Code)
				require.Equal(t, sent.Public(), got.Message)
				require.Empty(t, got.Reason)
				require.Empty(t, got.Violations)
				require.ErrorIs(t, got, apperr.New(code))
			})
		}
	})

	t.Run("custom message is kept", func(t *testing.T) {
		t.Parallel()

		wire := serverSide(t, apperr.NotFound().WithMessage("order 42 missing"))

		got := mustApperr(t, grpcerr.Map(wire))

		require.Equal(t, apperr.CodeNotFound, got.Code)
		require.Equal(t, "order 42 missing", got.Public())
	})

	t.Run("reason is kept", func(t *testing.T) {
		t.Parallel()

		wire := serverSide(t, apperr.InvalidCredentials())

		got := mustApperr(t, grpcerr.Map(wire))

		require.Equal(t, apperr.CodeUnauthenticated, got.Code)
		require.Equal(t, apperr.ReasonInvalidCredentials, got.Reason)
		require.Equal(t, apperr.ReasonMessageInvalidCredentials, got.Public())
	})

	t.Run("violations are kept in order", func(t *testing.T) {
		t.Parallel()

		violations := []apperr.Violation{
			{Field: "items.0.qty", Message: "must be positive"},
			{Field: "address_id", Message: "cannot be blank"},
		}

		wire := serverSide(t, apperr.ValidationFailed(violations...))

		got := mustApperr(t, grpcerr.Map(wire))

		require.Equal(t, apperr.CodeInvalidArgument, got.Code)
		require.Equal(t, apperr.ReasonValidationFailed, got.Reason)
		require.Equal(t, violations, got.Violations)
	})

	t.Run("wrapped apperr keeps code across the wire", func(t *testing.T) {
		t.Parallel()

		handlerErr := errors.Join(errors.New("repo"), apperr.AlreadyExists())

		wire := serverSide(t, handlerErr)

		got := mustApperr(t, grpcerr.Map(wire))

		require.Equal(t, apperr.CodeAlreadyExists, got.Code)
	})

	t.Run("internal cause does not cross the wire", func(t *testing.T) {
		t.Parallel()

		wire := serverSide(t, apperr.Internal().Wrap(errors.New("secret dsn")))

		got := mustApperr(t, grpcerr.Map(wire))

		require.Equal(t, apperr.CodeInternal, got.Code)
		require.Equal(t, apperr.MessageInternal, got.Public())
		require.NotContains(t, got.Error(), "secret dsn")
	})

	t.Run("plain error becomes internal", func(t *testing.T) {
		t.Parallel()

		wire := serverSide(t, errors.New("boom"))

		got := mustApperr(t, grpcerr.Map(wire))

		require.Equal(t, apperr.CodeInternal, got.Code)
		require.NotContains(t, got.Error(), "boom")
	})

	t.Run("context cancellation keeps its code", func(t *testing.T) {
		t.Parallel()

		wire := serverSide(t, context.Canceled)

		got := mustApperr(t, grpcerr.Map(wire))

		require.Equal(t, apperr.CodeCanceled, got.Code)
	})
}
