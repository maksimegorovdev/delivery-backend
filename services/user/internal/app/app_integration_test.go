//go:build integration

package app_test

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"
	userv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/user/v1"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/app"
)

const migrationsDir = "../../migrations/pg"

// appName помечает соединения приложения в pg_stat_activity, чтобы отличать их от соединений пула pgtest.
const appName = "user-app-test"

// Фиксированные идентификаторы собственных фикстур: на seed-миграции тесты не опираются.
const (
	smokeUserID    = "0190b100-0000-7000-8000-000000000001"
	smokeEmail     = "smoke.user@fixture.test"
	smokeFirstName = "Smoke"
	smokeLastName  = "User"
)

const (
	eventuallyTimeout = 10 * time.Second
	eventuallyTick    = 50 * time.Millisecond
)

// dsnOf собирает DSN к той же БД, что и пул pgtest: приложению нужна строка подключения, а не пул.
func dsnOf(pool *pgxpool.Pool) string {
	cc := pool.Config().ConnConfig

	u := url.URL{
		Scheme:   "postgresql",
		User:     url.UserPassword(cc.User, cc.Password),
		Host:     net.JoinHostPort(cc.Host, strconv.Itoa(int(cc.Port))),
		Path:     "/" + cc.Database,
		RawQuery: url.Values{"sslmode": {"disable"}, "application_name": {appName}}.Encode(),
	}

	return u.String()
}

// setEnv задаёт окружение приложения; телеметрия выключена, адрес gRPC-сервера — grpcAddr.
func setEnv(t *testing.T, dsn, grpcAddr string) {
	t.Helper()

	t.Setenv("APP_NAME", "user")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("PG_DSN", dsn)
	t.Setenv("PG_MAX_CONNS", "4")
	t.Setenv("GRPC_SERVER_ADDR", grpcAddr)
	t.Setenv("GRPC_SERVER_REFLECTION", "true")
	t.Setenv("OTEL_ENABLED", "false")
}

//nolint:paralleltest // t.Setenv
func TestApp_Smoke(t *testing.T) {
	pool := pgtest.NewDB(t, migrationsDir)

	_, err := pool.Exec(
		t.Context(),
		`INSERT INTO users (id, email, first_name, last_name) VALUES ($1, $2, $3, $4)`,
		smokeUserID, smokeEmail, smokeFirstName, smokeLastName,
	)
	require.NoError(t, err)

	setEnv(t, dsnOf(pool), "127.0.0.1:0")

	a, err := app.New(t.Context())
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(runCtx) }()

	conn, err := grpc.NewClient(a.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	var resp *userv1.GetUserResponse
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var callErr error
		resp, callErr = userv1.NewUserServiceClient(conn).GetUser(t.Context(), &userv1.GetUserRequest{Id: smokeUserID})
		assert.NoError(c, callErr)
	}, eventuallyTimeout, eventuallyTick)
	require.Equal(t, smokeUserID, resp.GetUser().GetId())
	require.Equal(t, smokeEmail, resp.GetUser().GetEmail())

	cancel()
	require.NoError(t, <-runDone)
	require.NoError(t, a.Close(t.Context()))
}

//nolint:paralleltest // t.Setenv
func TestApp_New_AddrInUse(t *testing.T) {
	pool := pgtest.NewDB(t, migrationsDir)

	var lc net.ListenConfig
	busy, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = busy.Close() })

	setEnv(t, dsnOf(pool), busy.Addr().String())

	a, err := app.New(t.Context())
	require.Error(t, err)
	require.Nil(t, a)

	// Ресурсы освобождены: пул приложения закрыт, его соединений в БД не осталось.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var conns int
		queryErr := pool.QueryRow(t.Context(),
			`SELECT count(*) FROM pg_stat_activity WHERE application_name = $1`, appName,
		).Scan(&conns)
		assert.NoError(c, queryErr)
		assert.Zero(c, conns)
	}, eventuallyTimeout, eventuallyTick)
}
