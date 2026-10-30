package client_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

// Времена различаются, чтобы перепутанные поля CreatedAt/UpdatedAt не прошли незамеченными.
func createdAt() time.Time { return time.Date(2025, time.March, 1, 10, 0, 0, 0, time.UTC) }

func updatedAt() time.Time { return time.Date(2025, time.March, 2, 11, 30, 0, 0, time.UTC) }

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

// errNotFound — ошибка, которую фейковый сервер возвращает клиенту.
func errNotFound() error { return status.Error(codes.NotFound, "not found") }

func requireNotFound(t *testing.T, err error) {
	t.Helper()

	e, ok := apperr.As(err)
	require.True(t, ok, "expected *apperr.Error in chain, got %T", err)
	require.Equal(t, apperr.CodeNotFound, e.Code)
}
