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
	productv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/product/v1"

	repo "github.com/maksimegorovdev/delivery-backend/services/product/internal/repository/pg"
	"github.com/maksimegorovdev/delivery-backend/services/product/internal/service"
	grpctransport "github.com/maksimegorovdev/delivery-backend/services/product/internal/transport/grpc"
)

const migrationsDir = "../../../migrations/pg"

// Фиксированные идентификаторы собственных фикстур: на seed-миграции тесты не опираются.
const (
	stackProductID      = "0190c000-0000-7000-8000-000000000001"
	stackOtherProductID = "0190c000-0000-7000-8000-000000000002"
	stackProductName    = "Fixture Pizza"
	stackProductPrice   = int64(49900)

	// stackMissingID — корректный UUID, которого нет в БД.
	stackMissingID = "0190c000-0000-7000-8000-0000000000ff"
)

// stackCreatedAt — время создания фикстур; updated_at на час позже, чтобы перепутанные поля не прошли незамеченными.
func stackCreatedAt() time.Time {
	return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
}

func stackUpdatedAt() time.Time {
	return stackCreatedAt().Add(time.Hour)
}

// newProductClient собирает боевой стек `product` так же, как app: интерцепторы Error → Logger → Validation,
// реальные сервис и репозиторий на pgtest. В БД уже лежат два товара: stackProductID и stackOtherProductID.
func newProductClient(t *testing.T) productv1.ProductServiceClient {
	t.Helper()

	pool := pgtest.NewDB(t, migrationsDir)
	seedStack(t, pool)

	validator, err := protovalidate.New()
	require.NoError(t, err)

	conn := grpctest.NewConn(t,
		func(s *grpc.Server) {
			grpctransport.NewRouter(grpctransport.RouterDeps{
				Server:         s,
				ProductService: service.NewProductService(repo.NewProductRepo(pool)),
			})
		},
		grpc.ChainUnaryInterceptor(
			interceptors.Error(),
			interceptors.Logger(slog.New(slog.DiscardHandler)),
			interceptors.Validation(validator),
		),
	)

	return productv1.NewProductServiceClient(conn)
}

func seedStack(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	for _, p := range []struct {
		id    string
		name  string
		price int64
	}{
		{stackProductID, stackProductName, stackProductPrice},
		{stackOtherProductID, "Fixture Burger", 29900},
	} {
		_, err := pool.Exec(
			t.Context(),
			`INSERT INTO products (id, name, price, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5)`,
			p.id,
			p.name,
			p.price,
			stackCreatedAt(),
			stackUpdatedAt(),
		)
		require.NoError(t, err)
	}
}
