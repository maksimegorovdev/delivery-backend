# Delivery Platform — Маппинг Domain ↔ DB и работа с NULL

Как в репозиторном слое читать строки из Postgres и превращать их в доменные сущности.
Стек: `pgx/v5` (`pgxpool`) — см. `platform/postgres`. Здесь описано, почему нужна отдельная
row-модель, как обрабатывать nullable-колонки именно в pgx (а не в `database/sql`) и почему
методы репозитория возвращают значение, а не указатель.

Принцип: доменный пакет ничего не знает про БД. Он не импортирует `pgx` / `pgtype`, в нём
нет `sql.Null*`, тегов `db:"..."` и представления времени из драйвера. Всё это живёт в
неэкспортируемой row-модели рядом с репозиторием, а наружу отдаётся чистая доменная
структура.

---

## 1. Две структуры: `userRow` и `domain.User`

Структура для `Scan` может отличаться от доменной сущности: в ней появляются
nullable-типы, технические поля, JSON и «сырое» представление времени. Явный маппинг
не даёт деталям БД протечь в бизнес-логику.

```go
// services/user/internal/domain/user.go — чистый домен, без pgx
package domain

import "time"

type User struct {
	ID        string
	Email     string
	FirstName string
	LastName  string
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt *time.Time — только если soft-delete нужен бизнес-логике наружу
}
```

```go
// services/user/internal/repository/user/postgres.go
package user

type userRow struct {
	ID        string     `db:"id"`
	Email     string     `db:"email"`
	FirstName string     `db:"first_name"`
	LastName  string     `db:"last_name"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt time.Time  `db:"updated_at"`
	DeletedAt *time.Time `db:"deleted_at"` // NULL -> nil автоматически
}

func (r userRow) toDomain() domain.User {
	return domain.User{
		ID:        r.ID,
		Email:     r.Email,
		FirstName: r.FirstName,
		LastName:  r.LastName,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}
```

Обе структуры и `toDomain` — неэкспортируемые, лежат в пакете репозитория.

---

## 2. NULL в pgx/v5 — не так, как в `database/sql`

Классический совет «SQL NULL нельзя сканировать в `string` / `int64` / `time.Time`,
используй `sql.NullString` / `sql.NullInt64` / `sql.NullTime`» — про `database/sql`.
В `pgx/v5` он не нужен: pgx сам кладёт `NULL` в **указатель** как `nil`.

| Колонка БД | Go-тип в row-модели | Комментарий |
|---|---|---|
| `text NOT NULL` | `string` | — |
| `text NULL` | `*string` | `NULL` → `nil`; иначе разыменование даёт значение |
| `bigint NOT NULL` | `int64` | — |
| `bigint NULL` | `*int64` | в `toDomain` разворачиваешь в доменный тип |
| `timestamptz NOT NULL` | `time.Time` | — |
| `timestamptz NULL` | `*time.Time` | не путать `NULL` и нулевое время `time.Time{}` |
| `uuid` | `string` | pgx v5 сканирует `uuid` в строку; альтернатива — `pgtype.UUID` |
| `jsonb` | `[]byte` / целевая структура | pgx умеет разворачивать JSON прямо в структуру |

`sql.NullString` и компания в pgx формально работают (через `sql.Scanner`), но это лишний
слой: `NullTime{Time, Valid}` всё равно приходится разворачивать в `*time.Time` для домена.
Указатель делает это сразу.

Альтернатива указателям — типы `pgtype` (`pgtype.Timestamptz`, `pgtype.Text`,
`pgtype.Int8`): более явно, но многословнее. Брать, только если нужны крайние случаи
Postgres (`infinity` у времени и т.п.). Для soft-delete и обычных nullable-полей
достаточно указателя.

### Запись

Обратное преобразование указателя pgx делает сам: `*time.Time == nil` → `NULL`,
`*string == nil` → `NULL`. Писать свой `driver.Valuer` не нужно.

---

## 3. Пример: чтение `users`

Таблица:

```sql
CREATE TABLE IF NOT EXISTS users (
    id         UUID PRIMARY KEY DEFAULT uuidv7(),
    email      TEXT NOT NULL,
    first_name TEXT NOT NULL,
    last_name  TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ DEFAULT NULL,

    CONSTRAINT chk_users_email CHECK (email = LOWER(email) AND LENGTH(email) <= 255),
    CONSTRAINT chk_users_first_name CHECK (LENGTH(first_name) <= 255),
    CONSTRAINT chk_users_last_name  CHECK (LENGTH(last_name)  <= 255)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_users_email_not_deleted
    ON users (email)
    WHERE deleted_at IS NULL;
```

Nullable-колонка здесь ровно одна — `deleted_at`. Всё остальное `NOT NULL`.

```go
func (repo *Repository) GetByID(ctx context.Context, id string) (domain.User, error) {
	rows, err := repo.pool.Query(ctx, `
		SELECT id, email, first_name, last_name, created_at, updated_at, deleted_at
		FROM users
		WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return domain.User{}, err
	}

	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[userRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, domain.ErrUserNotFound
		}
		return domain.User{}, err
	}
	return row.toDomain(), nil
}
```

`pgx.RowToStructByName` сопоставляет колонки с полями по тегу `db:"..."`; `*time.Time`
у `deleted_at` закрывает NULL. Для списка — `pgx.CollectRows(rows, pgx.RowToStructByName[userRow])`,
затем `map` по срезу через `toDomain`.

---

## 4. Нужен ли `deleted_at` в доменной модели

Сначала реши, где живёт soft-delete:

- **Забота репозитория.** Все запросы всегда идут с `WHERE deleted_at IS NULL`, наружу
  «удалённых» пользователей не бывает. Тогда в `domain.User` поля `DeletedAt` нет,
  nullable-типов в маппинге ноль. Поле остаётся только в `userRow` (или вовсе не
  выбирается в `SELECT`).
- **Забота бизнес-логики.** Нужно показывать «удалён / восстановить», фильтровать
  по-разному в разных сценариях — тогда `DeletedAt *time.Time` в домене оправдан.

По умолчанию — первый вариант.

### Когда отдельная row-модель избыточна

Если запрос возвращает ровно доменную форму и в нём **нет** ни nullable-, ни
технических колонок — можно сканировать прямо в доменную структуру. Но у `users` есть
`created_at` / `updated_at` / `deleted_at`, которые не хочется тащить в домен «как есть»,
поэтому здесь `userRow` оправдан.

---

## 5. Почему `GetByID` возвращает `domain.User`, а не `*domain.User`

### `nil` как «нет пользователя» конфликтует с ошибкой

Основная причина возвращать `*T` — сигналить отсутствие через `nil`. Но контракт метода
уже `(domain.User, error)`, и отсутствие выражено `domain.ErrUserNotFound`. С указателем
появляется два канала для «нет юзера»: `nil` и ошибка — что проверять вызывающему? Что
значит `err == nil` при `nil`-указателе?

Со значением контракт однозначный: `err == nil` ⇒ структура заполнена и валидна.

### Защита от паники

```go
user, err := repo.GetByID(ctx, id)
fmt.Println(user.Email) // забыли проверить err
```

Со значением это просто пустой `Email`. С `*domain.User` — `nil pointer dereference`
и падение сервиса.

### Копия здесь ничего не стоит

`domain.User` — несколько строк и пара `time.Time`, десятки байт. Копия при возврате
незаметна. Указатель, наоборот, почти всегда уводит структуру в heap (escape analysis)
и нагружает GC — по перформансу здесь он скорее хуже.

### Нет общего изменяемого состояния

Вызывающий получает свою копию. Нельзя случайно поменять поле и задеть кэш репозитория
или другую горутину.

### Когда `*T` оправдан

- Структура реально большая (десятки полей, вложенные срезы) и профайлер показывает,
  что копия дорогая.
- Тип нельзя копировать: содержит `sync.Mutex`, `sync.Once`, счётчик.
- `nil` — документированное «опционально», и канала ошибки нет (тогда обычно лучше `(T, bool)`).
- Метод должен мутировать конкретный хранимый экземпляр.

Для доменной сущности из репозитория ничего из этого обычно не выполняется → возвращаем
значение. Для списков по той же логике — `[]domain.User`, а не `[]*domain.User`.

---

## 6. Границы

- Пакет `domain` не импортирует `pgx` / `pgtype` — в этом весь смысл маппинга.
- `userRow`, `toDomain`, теги `db:"..."` — неэкспортируемые, в пакете репозитория.
- `pgx.ErrNoRows` не выходит за пределы репозитория: перехватывается и превращается в
  доменную ошибку (`NOT_FOUND`, см. [errors.md](errors.md)).
- UUID: если нужна типобезопасность вместо `string` — `github.com/google/uuid.UUID` в
  домене, а в `userRow` поле `pgtype.UUID` или тот же `string` с конвертацией в `toDomain`.
