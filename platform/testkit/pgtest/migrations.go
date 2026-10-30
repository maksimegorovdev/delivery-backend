package pgtest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
)

// leftoversQuery возвращает всё, что осталось в схеме public после отката, кроме служебной
// таблицы migrate. Индексы и сиквенсы serial-колонок удаляются вместе с таблицами и сюда не попадают.
const leftoversQuery = `
SELECT c.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public'
  AND c.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')
  AND c.relname <> 'schema_migrations'
UNION ALL
SELECT t.typname
FROM pg_type t
JOIN pg_namespace n ON n.oid = t.typnamespace
WHERE n.nspname = 'public'
  AND t.typtype IN ('e', 'd')
UNION ALL
SELECT p.proname
FROM pg_proc p
JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE n.nspname = 'public'
ORDER BY 1`

// CheckMigrations создаёт пустую БД в общем контейнере, применяет все миграции из migrationsDir,
// откатывает их до нуля и применяет снова. Падает, если любой шаг вернул ошибку или после полного
// отката в схеме public осталось что-то кроме служебной таблицы migrate.
//
// Использует тот же контейнер и то же правило «один каталог миграций на тестовый бинарь», что и [NewDB].
// БД создаётся без TEMPLATE и удаляется до возврата из функции, шаблон не затрагивается.
func CheckMigrations(tb testing.TB, migrationsDir string) {
	tb.Helper()

	srv, err := sharedServer(migrationsDir)
	if err != nil {
		tb.Fatalf("pgtest: %v", err)
	}

	name := "t_" + strings.ToLower(rand.Text())

	if err = checkMigrations(tb.Context(), srv, name, srv.migrationsDir); err != nil {
		tb.Fatalf("pgtest: check migrations: %v", err)
	}
}

// checkMigrations выполняет цикл up → down → проверка схемы → up в новой пустой БД name
// и удаляет её при любом исходе. dir должен быть абсолютным путём.
func checkMigrations(ctx context.Context, srv server, name, dir string) (err error) {
	if err = createEmptyDatabase(ctx, srv, name); err != nil {
		return err
	}
	defer func() {
		dropCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dbOpTimeout)
		defer cancel()

		err = errors.Join(err, dropDatabase(dropCtx, srv, name))
	}()

	dsn, err := databaseDSN(srv, name)
	if err != nil {
		return err
	}

	return roundTrip(ctx, dsn, dir)
}

func roundTrip(ctx context.Context, dsn, dir string) (err error) {
	source := url.URL{Scheme: "file", Path: filepath.ToSlash(dir)}
	database := strings.Replace(dsn, "postgres://", "pgx5://", 1)

	m, err := migrate.New(source.String(), database)
	if err != nil {
		return fmt.Errorf("init migrate for %s: %w", dir, err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		err = errors.Join(err, srcErr, dbErr)
	}()

	if err = runStep("up", m.Up); err != nil {
		return err
	}
	if err = runStep("down", m.Down); err != nil {
		return err
	}
	if err = assertSchemaEmpty(ctx, dsn); err != nil {
		return err
	}

	return runStep("up after down", m.Up)
}

// runStep выполняет шаг миграции; «нет изменений» не считается ошибкой.
func runStep(name string, step func() error) error {
	if err := step(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate %s: %w", name, err)
	}

	return nil
}

func assertSchemaEmpty(ctx context.Context, dsn string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to check schema: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	rows, err := conn.Query(ctx, leftoversQuery)
	if err != nil {
		return fmt.Errorf("query schema leftovers: %w", err)
	}

	leftovers, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("read schema leftovers: %w", err)
	}
	if len(leftovers) > 0 {
		return fmt.Errorf("schema public is not empty after down migrations: %s", strings.Join(leftovers, ", "))
	}

	return nil
}

// createEmptyDatabase создаёт БД без TEMPLATE, чтобы миграции применялись с нуля.
func createEmptyDatabase(ctx context.Context, srv server, name string) error {
	return execAdmin(ctx, srv, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
}

// databaseDSN возвращает DSN сервера, указывающий на БД name.
func databaseDSN(srv server, name string) (string, error) {
	u, err := url.Parse(srv.dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	u.Path = "/" + name

	return u.String(), nil
}
