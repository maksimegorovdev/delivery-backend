package interceptors_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/apperr/grpcerr"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/interceptors"
)

func failWith(err error) checkFunc {
	return func(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
		return nil, err
	}
}

func errorInfo(t *testing.T, st *status.Status) *errdetails.ErrorInfo {
	t.Helper()

	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			return info
		}
	}
	require.Fail(t, "ErrorInfo detail not found")

	return nil
}

func TestError_MapsEveryAppErrCodeToGRPC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code apperr.Code
		want codes.Code
	}{
		{name: "canceled", code: apperr.CodeCanceled, want: codes.Canceled},
		{name: "unknown", code: apperr.CodeUnknown, want: codes.Unknown},
		{name: "invalid argument", code: apperr.CodeInvalidArgument, want: codes.InvalidArgument},
		{name: "deadline exceeded", code: apperr.CodeDeadlineExceeded, want: codes.DeadlineExceeded},
		{name: "not found", code: apperr.CodeNotFound, want: codes.NotFound},
		{name: "already exists", code: apperr.CodeAlreadyExists, want: codes.AlreadyExists},
		{name: "permission denied", code: apperr.CodePermissionDenied, want: codes.PermissionDenied},
		{name: "resource exhausted", code: apperr.CodeResourceExhausted, want: codes.ResourceExhausted},
		{name: "failed precondition", code: apperr.CodeFailedPrecondition, want: codes.FailedPrecondition},
		{name: "aborted", code: apperr.CodeAborted, want: codes.Aborted},
		{name: "out of range", code: apperr.CodeOutOfRange, want: codes.OutOfRange},
		{name: "unimplemented", code: apperr.CodeUnimplemented, want: codes.Unimplemented},
		{name: "internal", code: apperr.CodeInternal, want: codes.Internal},
		{name: "unavailable", code: apperr.CodeUnavailable, want: codes.Unavailable},
		{name: "data loss", code: apperr.CodeDataLoss, want: codes.DataLoss},
		{name: "unauthenticated", code: apperr.CodeUnauthenticated, want: codes.Unauthenticated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, _ := newHealthClient(t, failWith(apperr.New(tt.code)), interceptors.Error())

			_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})

			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, tt.want, st.Code())
			assert.Equal(t, tt.code.Public(), st.Message())

			info := errorInfo(t, st)
			assert.Equal(t, tt.code.String(), info.GetReason())
			assert.Equal(t, grpcerr.Domain, info.GetDomain())
		})
	}
}

func TestError_PassesThroughSuccess(t *testing.T) {
	t.Parallel()

	client, _ := newHealthClient(t, okCheck, interceptors.Error())

	resp, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.GetStatus())
}

func TestError_UsesPublicMessageNotCause(t *testing.T) {
	t.Parallel()

	handlerErr := apperr.NotFound().WithMessage("user not found").Wrap(errors.New("select users: secret detail"))
	client, _ := newHealthClient(t, failWith(handlerErr), interceptors.Error())

	_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})

	st, _ := status.FromError(err)
	assert.Equal(t, "user not found", st.Message())
	assert.NotContains(t, st.Message(), "secret")
}

func TestError_AddsReasonMetadata(t *testing.T) {
	t.Parallel()

	handlerErr := apperr.InvalidCredentials()
	client, _ := newHealthClient(t, failWith(handlerErr), interceptors.Error())

	_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})

	st, _ := status.FromError(err)
	assert.Equal(t, apperr.ReasonInvalidCredentials, errorInfo(t, st).GetMetadata()["reason"])
}

func TestError_OmitsReasonMetadataWhenEmpty(t *testing.T) {
	t.Parallel()

	client, _ := newHealthClient(t, failWith(apperr.NotFound()), interceptors.Error())

	_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})

	st, _ := status.FromError(err)
	assert.Empty(t, errorInfo(t, st).GetMetadata())
}

func TestError_AddsFieldViolations(t *testing.T) {
	t.Parallel()

	handlerErr := apperr.ValidationFailed(
		apperr.Violation{Field: "name", Message: "must not be empty"},
		apperr.Violation{Field: "age", Message: "must be positive"},
	)
	client, _ := newHealthClient(t, failWith(handlerErr), interceptors.Error())

	_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})

	st, _ := status.FromError(err)
	var got []*errdetails.BadRequest_FieldViolation
	for _, d := range st.Details() {
		if br, ok := d.(*errdetails.BadRequest); ok {
			got = br.GetFieldViolations()
		}
	}
	require.Len(t, got, 2)
	assert.Equal(t, "name", got[0].GetField())
	assert.Equal(t, "must not be empty", got[0].GetDescription())
	assert.Equal(t, "age", got[1].GetField())
	assert.Equal(t, "must be positive", got[1].GetDescription())
}

func TestError_HidesPlainErrorAsInternal(t *testing.T) {
	t.Parallel()

	client, _ := newHealthClient(t, failWith(errors.New("connection refused to 10.0.0.1")), interceptors.Error())

	_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})

	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Equal(t, apperr.MessageInternal, st.Message())
}

func TestError_MapsContextErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{name: "context canceled", err: context.Canceled, want: codes.Canceled},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: codes.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, _ := newHealthClient(t, failWith(tt.err), interceptors.Error())

			_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})

			assert.Equal(t, tt.want, status.Code(err))
		})
	}
}
