// Package pgtest даёт интеграционным тестам изолированную БД Postgres с применёнными миграциями.
//
// На тестовый бинарь запускается один контейнер. Миграции применяются один раз в шаблонную БД,
// а каждый [NewDB] клонирует её через CREATE DATABASE ... TEMPLATE, поэтому тесты можно
// запускать параллельно. Пакет импортируется только из *_test.go.
package pgtest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // драйвер БД для migrate
	_ "github.com/golang-migrate/migrate/v4/source/file"     // источник миграций из каталога
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	// Образ совпадает с docker-compose.yml.
	image = "postgres:18"

	templateDB = "template_app"
	adminDB    = "postgres"
	dbUser     = "postgres"
	dbPassword = "postgres"

	// maxPoolConns ограничивает пул одного теста: параллельные тесты не должны упереться
	// в max_connections общего контейнера.
	maxPoolConns = 4

	startTimeout = 2 * time.Minute
	dbOpTimeout  = 30 * time.Second
)

// server описывает запущенный контейнер с применёнными в шаблонную БД миграциями.
type server struct {
	dsn           string // подключение к БД template_app
	migrationsDir string // абсолютный путь каталога, из которого собран шаблон
}

//nolint:gochecknoglobals // один контейнер на тестовый бинарь, иначе каждый тест платит за его запуск
var (
	once      sync.Once
	shared    server
	errShared error
)

// NewDB возвращает пул к новой БД, склонированной из шаблона с миграциями из migrationsDir.
//
// Путь относительный: тест запускается из каталога своего пакета. В пределах одного тестового
// бинаря каталог должен быть один и тот же, потому что шаблон собирается по первому вызову.
// БД удаляется в tb.Cleanup. Если Docker недоступен, тест падает, а не пропускается.
func NewDB(tb testing.TB, migrationsDir string) *pgxpool.Pool {
	tb.Helper()

	srv, err := sharedServer(migrationsDir)
	if err != nil {
		tb.Fatalf("pgtest: %v", err)
	}

	name := "t_" + strings.ToLower(rand.Text())

	if err = createDatabase(tb.Context(), srv, name); err != nil {
		tb.Fatalf("pgtest: %v", err)
	}
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(tb.Context()), dbOpTimeout)
		defer cancel()

		if dropErr := dropDatabase(ctx, srv, name); dropErr != nil {
			tb.Errorf("pgtest: %v", dropErr)
		}
	})

	poolCfg, err := pgxpool.ParseConfig(srv.dsn)
	if err != nil {
		tb.Fatalf("pgtest: parse dsn: %v", err)
	}
	poolCfg.ConnConfig.Database = name
	poolCfg.MaxConns = maxPoolConns

	pool, err := pgxpool.NewWithConfig(tb.Context(), poolCfg)
	if err != nil {
		tb.Fatalf("pgtest: connect to %s: %v", name, err)
	}
	tb.Cleanup(pool.Close) // выполняется до удаления БД: cleanup идут в обратном порядке

	return pool
}

// sharedServer запускает контейнер при первом вызове и возвращает его всем последующим.
func sharedServer(migrationsDir string) (server, error) {
	absDir, err := filepath.Abs(migrationsDir)
	if err != nil {
		return server{}, fmt.Errorf("resolve migrations dir %q: %w", migrationsDir, err)
	}

	once.Do(func() {
		shared, errShared = start(absDir)
	})
	if errShared != nil {
		return server{}, errShared
	}
	if shared.migrationsDir != absDir {
		return server{}, fmt.Errorf(
			"template database is built from %q, got another migrations dir %q",
			shared.migrationsDir,
			absDir,
		)
	}

	return shared, nil
}

// start поднимает контейнер и применяет миграции в шаблонную БД.
// Контейнер удаляет Ryuk после завершения тестового бинаря.
func start(migrationsDir string) (server, error) {
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	ctr, err := postgres.Run(ctx, image,
		postgres.WithDatabase(templateDB),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPassword),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		_ = testcontainers.TerminateContainer(ctr)

		return server{}, fmt.Errorf("run %s container (is docker running?): %w", image, err)
	}

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(ctr)

		return server{}, fmt.Errorf("get connection string: %w", err)
	}

	if err = applyMigrations(dsn, migrationsDir); err != nil {
		_ = testcontainers.TerminateContainer(ctr)

		return server{}, err
	}

	return server{dsn: dsn, migrationsDir: migrationsDir}, nil
}

// applyMigrations применяет миграции и закрывает все соединения с БД,
// иначе её нельзя будет использовать как шаблон.
func applyMigrations(dsn, dir string) error {
	source := url.URL{Scheme: "file", Path: filepath.ToSlash(dir)}
	database := strings.Replace(dsn, "postgres://", "pgx5://", 1)

	m, err := migrate.New(source.String(), database)
	if err != nil {
		return fmt.Errorf("init migrate for %s: %w", dir, err)
	}

	upErr := m.Up()
	if errors.Is(upErr, migrate.ErrNoChange) {
		upErr = nil
	}
	srcErr, dbErr := m.Close()

	if joined := errors.Join(upErr, srcErr, dbErr); joined != nil {
		return fmt.Errorf("apply migrations from %s: %w", dir, joined)
	}

	return nil
}

func createDatabase(ctx context.Context, srv server, name string) error {
	return execAdmin(
		ctx,
		srv,
		"CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{templateDB}.Sanitize(),
	)
}

func dropDatabase(ctx context.Context, srv server, name string) error {
	return execAdmin(ctx, srv, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
}

// execAdmin выполняет запрос от имени отдельного соединения с служебной БД.
func execAdmin(ctx context.Context, srv server, query string) error {
	cfg, err := pgx.ParseConfig(srv.dsn)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}
	cfg.Database = adminDB

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect to %s database: %w", adminDB, err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	if _, err = conn.Exec(ctx, query); err != nil {
		return fmt.Errorf("exec %q: %w", query, err)
	}

	return nil
}
