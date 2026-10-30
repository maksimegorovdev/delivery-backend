//go:build integration

package pg_test

const migrationsDir = "../../../migrations/pg"

// Фиксированные идентификаторы собственных фикстур: на seed-миграции тесты не опираются.
const (
	fixtureEventID      = "0190a000-0000-7000-8000-000000000301"
	fixtureOtherEventID = "0190a000-0000-7000-8000-000000000302"
	fixtureAggregateID  = "0190a000-0000-7000-8000-000000000101"
)
