package apperr_test

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

type codeCase struct {
	name   string
	code   apperr.Code
	str    string
	grpc   codes.Code
	http   int
	level  slog.Level
	public string
}

func codeCases() []codeCase {
	return []codeCase{
		{"canceled", apperr.CodeCanceled, "CANCELED", codes.Canceled, 499, slog.LevelWarn, apperr.MessageCanceled},
		{
			"unknown",
			apperr.CodeUnknown,
			"UNKNOWN",
			codes.Unknown,
			http.StatusInternalServerError,
			slog.LevelError,
			apperr.MessageInternal,
		},
		{
			"invalid argument",
			apperr.CodeInvalidArgument,
			"INVALID_ARGUMENT",
			codes.InvalidArgument,
			http.StatusBadRequest,
			slog.LevelWarn,
			apperr.MessageInvalidArgument,
		},
		{
			"deadline exceeded",
			apperr.CodeDeadlineExceeded,
			"DEADLINE_EXCEEDED",
			codes.DeadlineExceeded,
			http.StatusGatewayTimeout,
			slog.LevelError,
			apperr.MessageDeadlineExceeded,
		},
		{
			"not found",
			apperr.CodeNotFound,
			"NOT_FOUND",
			codes.NotFound,
			http.StatusNotFound,
			slog.LevelWarn,
			apperr.MessageNotFound,
		},
		{
			"already exists",
			apperr.CodeAlreadyExists,
			"ALREADY_EXISTS",
			codes.AlreadyExists,
			http.StatusConflict,
			slog.LevelWarn,
			apperr.MessageAlreadyExists,
		},
		{
			"permission denied",
			apperr.CodePermissionDenied,
			"PERMISSION_DENIED",
			codes.PermissionDenied,
			http.StatusForbidden,
			slog.LevelWarn,
			apperr.MessagePermissionDenied,
		},
		{
			"resource exhausted",
			apperr.CodeResourceExhausted,
			"RESOURCE_EXHAUSTED",
			codes.ResourceExhausted,
			http.StatusTooManyRequests,
			slog.LevelWarn,
			apperr.MessageResourceExhausted,
		},
		{
			"failed precondition",
			apperr.CodeFailedPrecondition,
			"FAILED_PRECONDITION",
			codes.FailedPrecondition,
			http.StatusBadRequest,
			slog.LevelWarn,
			apperr.MessageFailedPrecondition,
		},
		{
			"aborted",
			apperr.CodeAborted,
			"ABORTED",
			codes.Aborted,
			http.StatusConflict,
			slog.LevelWarn,
			apperr.MessageAborted,
		},
		{
			"out of range",
			apperr.CodeOutOfRange,
			"OUT_OF_RANGE",
			codes.OutOfRange,
			http.StatusBadRequest,
			slog.LevelWarn,
			apperr.MessageOutOfRange,
		},
		{
			"unimplemented",
			apperr.CodeUnimplemented,
			"UNIMPLEMENTED",
			codes.Unimplemented,
			http.StatusNotImplemented,
			slog.LevelError,
			apperr.MessageUnimplemented,
		},
		{
			"internal",
			apperr.CodeInternal,
			"INTERNAL",
			codes.Internal,
			http.StatusInternalServerError,
			slog.LevelError,
			apperr.MessageInternal,
		},
		{
			"unavailable",
			apperr.CodeUnavailable,
			"UNAVAILABLE",
			codes.Unavailable,
			http.StatusServiceUnavailable,
			slog.LevelError,
			apperr.MessageUnavailable,
		},
		{
			"data loss",
			apperr.CodeDataLoss,
			"DATA_LOSS",
			codes.DataLoss,
			http.StatusInternalServerError,
			slog.LevelError,
			apperr.MessageInternal,
		},
		{
			"unauthenticated",
			apperr.CodeUnauthenticated,
			"UNAUTHENTICATED",
			codes.Unauthenticated,
			http.StatusUnauthorized,
			slog.LevelWarn,
			apperr.MessageUnauthenticated,
		},
	}
}

func TestCode(t *testing.T) {
	t.Parallel()

	for _, tc := range codeCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.str, tc.code.String())
			require.Equal(t, tc.grpc, tc.code.GRPC())
			require.Equal(t, tc.http, tc.code.HTTP())
			require.Equal(t, tc.level, tc.code.Level())
			require.Equal(t, tc.public, tc.code.Public())
		})
	}

	t.Run("unknown code falls back to internal", func(t *testing.T) {
		t.Parallel()

		code := apperr.Code("SOMETHING_NEW")

		require.Equal(t, codes.Internal, code.GRPC())
		require.Equal(t, http.StatusInternalServerError, code.HTTP())
		require.Equal(t, slog.LevelWarn, code.Level())
		require.Equal(t, apperr.MessageInternal, code.Public())
	})
}

func TestCodeFromGRPC(t *testing.T) {
	t.Parallel()

	for _, tc := range codeCases() {
		t.Run("round trip "+tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.code, apperr.CodeFromGRPC(tc.grpc))
		})
	}

	t.Run("ok maps to empty code", func(t *testing.T) {
		t.Parallel()

		require.Empty(t, apperr.CodeFromGRPC(codes.OK))
	})

	t.Run("unknown grpc code maps to internal", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, apperr.CodeInternal, apperr.CodeFromGRPC(codes.Code(1000)))
	})
}
