//go:build integration

package grpc_test

import (
	"log/slog"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/maksimegorovdev/delivery-backend/platform/grpc/interceptors"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/grpctest"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"
	addressv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/address/v1"
	userv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/user/v1"

	repo "github.com/maksimegorovdev/delivery-backend/services/user/internal/repository/pg"
	"github.com/maksimegorovdev/delivery-backend/services/user/internal/service"
	grpctransport "github.com/maksimegorovdev/delivery-backend/services/user/internal/transport/grpc"
)

const migrationsDir = "../../../migrations/pg"

// Фиксированные идентификаторы собственных фикстур: на seed-миграции тесты не опираются.
const (
	stackUserID    = "0190b000-0000-7000-8000-000000000001"
	stackAddressID = "0190b000-0000-7000-8000-000000000011"
	stackEmail     = "ivan.petrov@fixture.test"
	stackFirstName = "Ivan"
	stackLastName  = "Petrov"
	stackAddress   = "Lenina st, 1"

	// stackMissingID — корректный UUID, которого нет в БД.
	stackMissingID = "0190b000-0000-7000-8000-0000000000ff"
)

// stackCreatedAt — время создания фикстур; updated_at на час позже, чтобы перепутанные поля не прошли незамеченными.
func stackCreatedAt() time.Time {
	return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
}

func stackUpdatedAt() time.Time {
	return stackCreatedAt().Add(time.Hour)
}

// userStack — клиенты боевого gRPC-стека `user` поверх реальной БД.
type userStack struct {
	users     userv1.UserServiceClient
	addresses addressv1.AddressServiceClient
}

// newUserStack собирает боевой стек `user` так же, как app: интерцепторы Error → Logger → Validation,
// реальные сервисы и репозитории на pgtest. В БД уже лежат фикстуры stackUserID и stackAddressID.
func newUserStack(t *testing.T) userStack {
	t.Helper()

	pool := pgtest.NewDB(t, migrationsDir)
	seedStack(t, pool)

	validator, err := protovalidate.New()
	require.NoError(t, err)

	conn := grpctest.NewConn(t,
		func(s *grpc.Server) {
			grpctransport.NewRouter(grpctransport.RouterDeps{
				Server:         s,
				UserService:    service.NewUserService(repo.NewUserRepo(pool)),
				AddressService: service.NewAddressService(repo.NewAddressRepo(pool)),
			})
		},
		grpc.ChainUnaryInterceptor(
			interceptors.Error(),
			interceptors.Logger(slog.New(slog.DiscardHandler)),
			interceptors.Validation(validator),
		),
	)

	return userStack{
		users:     userv1.NewUserServiceClient(conn),
		addresses: addressv1.NewAddressServiceClient(conn),
	}
}

func seedStack(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	_, err := pool.Exec(
		t.Context(),
		`INSERT INTO users (id, email, first_name, last_name, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		stackUserID,
		stackEmail,
		stackFirstName,
		stackLastName,
		stackCreatedAt(),
		stackUpdatedAt(),
	)
	require.NoError(t, err)

	_, err = pool.Exec(
		t.Context(),
		`INSERT INTO addresses (id, user_id, address, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)`,
		stackAddressID,
		stackUserID,
		stackAddress,
		stackCreatedAt(),
		stackUpdatedAt(),
	)
	require.NoError(t, err)
}
