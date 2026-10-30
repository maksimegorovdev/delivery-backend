package pgerr_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/apperr/pgerr"
)

func pgError(code string) *pgconn.PgError {
	return &pgconn.PgError{Code: code, Message: "pg message"}
}

func TestMap(t *testing.T) {
	t.Parallel()

	t.Run("nil stays nil", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, pgerr.Map(nil))
	})

	t.Run("apperr passes through untouched", func(t *testing.T) {
		t.Parallel()

		src := apperr.PermissionDenied().WithMessage("nope")

		require.Same(t, src, pgerr.Map(src))
	})

	t.Run("wrapped apperr is returned as is", func(t *testing.T) {
		t.Parallel()

		src := fmt.Errorf("service: %w", apperr.NotFound())

		require.Equal(t, src, pgerr.Map(src))
	})

	tests := []struct {
		name string
		err  error
		want apperr.Code
	}{
		{"no rows", pgx.ErrNoRows, apperr.CodeNotFound},
		{"wrapped no rows", fmt.Errorf("get order: %w", pgx.ErrNoRows), apperr.CodeNotFound},
		{"context canceled", context.Canceled, apperr.CodeCanceled},
		{"context deadline exceeded", context.DeadlineExceeded, apperr.CodeDeadlineExceeded},
		{"unique violation", pgError(pgerrcode.UniqueViolation), apperr.CodeAlreadyExists},
		{"exclusion violation", pgError(pgerrcode.ExclusionViolation), apperr.CodeAlreadyExists},
		{
			"wrapped unique violation",
			fmt.Errorf("insert: %w", pgError(pgerrcode.UniqueViolation)),
			apperr.CodeAlreadyExists,
		},
		{"foreign key violation", pgError(pgerrcode.ForeignKeyViolation), apperr.CodeInvalidArgument},
		{"not null violation", pgError(pgerrcode.NotNullViolation), apperr.CodeInvalidArgument},
		{"check violation", pgError(pgerrcode.CheckViolation), apperr.CodeInvalidArgument},
		{"invalid text representation", pgError(pgerrcode.InvalidTextRepresentation), apperr.CodeInvalidArgument},
		{
			"string data right truncation",
			pgError(pgerrcode.StringDataRightTruncationDataException),
			apperr.CodeInvalidArgument,
		},
		{"query canceled", pgError(pgerrcode.QueryCanceled), apperr.CodeDeadlineExceeded},
		{"connection failure", pgError(pgerrcode.ConnectionFailure), apperr.CodeUnavailable},
		{"serialization failure", pgError(pgerrcode.SerializationFailure), apperr.CodeUnavailable},
		{"deadlock detected", pgError(pgerrcode.DeadlockDetected), apperr.CodeUnavailable},
		{"too many connections", pgError(pgerrcode.TooManyConnections), apperr.CodeUnavailable},
		{"lock not available", pgError(pgerrcode.LockNotAvailable), apperr.CodeUnavailable},
		{"admin shutdown", pgError(pgerrcode.AdminShutdown), apperr.CodeUnavailable},
		{"undefined table", pgError(pgerrcode.UndefinedTable), apperr.CodeInternal},
		{"syntax error", pgError(pgerrcode.SyntaxError), apperr.CodeInternal},
		{"empty sqlstate", pgError(""), apperr.CodeInternal},
		{"one-char sqlstate", pgError("2"), apperr.CodeInternal},
		{"non-pg error", errors.New("boom"), apperr.CodeInternal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := pgerr.Map(tc.err)

			appErr, ok := apperr.As(got)
			require.True(t, ok, "expected *apperr.Error, got %T", got)
			require.Equal(t, tc.want, appErr.Code)
			require.ErrorIs(t, got, tc.err, "cause must stay reachable")
		})
	}

	t.Run("pg error is reachable with errors.As", func(t *testing.T) {
		t.Parallel()

		got := pgerr.Map(fmt.Errorf("insert: %w", pgError(pgerrcode.UniqueViolation)))

		var pg *pgconn.PgError
		require.ErrorAs(t, got, &pg)
		require.Equal(t, pgerrcode.UniqueViolation, pg.Code)
	})

	t.Run("internal error does not leak driver text to clients", func(t *testing.T) {
		t.Parallel()

		got, ok := apperr.As(pgerr.Map(pgError(pgerrcode.UndefinedTable)))

		require.True(t, ok)
		require.Equal(t, apperr.MessageInternal, got.Public())
	})
}
