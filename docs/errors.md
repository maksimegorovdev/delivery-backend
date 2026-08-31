# Delivery Platform — Ошибки: коды и маппинг

Справочник по пакету `platform/apperr`: значение каждого доменного кода, его отображение
в gRPC-статус, HTTP-статус и уровень лога, а также обратный маппинг из gRPC и из Postgres.

Принцип: через все слои идёт одна доменная ошибка `*apperr.Error`. У неё два лица —
`Message` (безопасный текст для клиента) и `Err` (внутренняя цепочка причин, только в логи).
Весь маппинг между кодами живёт рядом с типом `Code`, транспортные пакеты друг о друге не знают.

---

## 1. Доменные коды `apperr.Code`

| Код | Значение | Когда возвращать | Типичный источник |
|---|---|---|---|
| `INTERNAL` | Непредвиденная ошибка сервера. Клиент ничего не может с этим сделать. Наружу — только generic-текст, детали в лог. | Любая незапланированная ошибка, паника, «этого не должно было случиться», неизвестная ошибка от зависимости. | Баг в коде, сбой сериализации, неизвестный SQLSTATE, `nil`-разыменование. |
| `INVALID_ARGUMENT` | Аргумент запроса не прошёл проверку бизнес-правил. Запрос синтаксически корректен, но семантически неверен. | Значение вне допустимого диапазона, ссылка на несуществующую сущность в поле, нарушение инварианта домена. | Валидация в service-слое, `FOREIGN KEY` / `CHECK` / `NOT NULL` от Postgres. |
| `INVALID_REQUEST_BODY` | Тело запроса не удалось разобрать: не JSON, не тот тип поля, не распарсился protobuf. Ошибка на уровне формата, не бизнес-логики. | Ошибка `json.Unmarshal`, отсутствует обязательное поле на уровне DTO, неверный `Content-Type`. | HTTP-хендлер (gateway), декодирование запроса. |
| `VALIDATION_FAILED` | Составная ошибка валидации: несколько полей невалидны одновременно. Несёт список `Violations` (поле → сообщение), безопасный для клиента. | Валидация формы/DTO, где нужно вернуть все ошибки сразу, а не первую. | Валидатор DTO в хендлере или service-слое. |
| `NOT_FOUND` | Запрошенная сущность не существует или недоступна текущему пользователю (чтобы не раскрывать существование). | `GetByID` не нашёл строку, переход по несуществующему идентификатору. | `pgx.ErrNoRows` в репозитории. |
| `ALREADY_EXISTS` | Сущность с такими уникальными атрибутами уже есть. Повторное создание. | Нарушение `UNIQUE`-индекса при вставке, повторная регистрация. | `UNIQUE_VIOLATION` (SQLSTATE `23505`). |
| `CONFLICT` | Состояние ресурса не позволяет выполнить операцию сейчас; имеет смысл повторить после изменения состояния. Отличается от `ALREADY_EXISTS` тем, что это не про уникальность, а про конкурентный доступ / порядок операций. | Оптимистическая блокировка (версия изменилась), заказ уже оплачен/отменён, race при переходе статуса. | `SERIALIZATION_FAILURE` / `DEADLOCK_DETECTED` (если решено считать их конфликтом, а не `UNAVAILABLE`), проверка версии в service-слое. |
| `UNAUTHENTICATED` | Запрос без валидных учётных данных: токен отсутствует, истёк, подпись неверна. Клиент не идентифицирован. | Middleware аутентификации, невалидный / отсутствующий `Authorization`. | Auth-middleware gateway. |
| `INVALID_CREDENTIALS` | Учётные данные предоставлены, но не совпали (неверный логин/пароль). Подвид `UNAUTHENTICATED` с отдельным кодом для логина. | Хендлер логина: пользователь не найден или пароль не сошёлся. Наружу — всегда одинаковый текст, без указания, что именно не так. | Service-слой аутентификации. |
| `FORBIDDEN` | Клиент идентифицирован, но не имеет прав на операцию/ресурс. | Проверка ролей/владения ресурсом: пользователь пытается прочитать чужой заказ. | Проверка доступа в service-слое. |
| `TIMEOUT` | Операция не уложилась в дедлайн: истёк `context.Context`, отменён запрос, долгий запрос к БД был прерван. | Дедлайн вызова превышен, `context.DeadlineExceeded`, отменённый пользователем запрос. | `context` отменён, `QUERY_CANCELED` (SQLSTATE `57014`), таймаут вызова зависимости. |
| `UNAVAILABLE` | Зависимость временно недоступна: нет соединения с БД/Kafka/соседним сервисом, пул исчерпан. Операцию имеет смысл повторить с backoff. | Не удалось подключиться к Postgres, gRPC-вызов вернул `Unavailable`, пул соединений пуст. | Ошибка dial/connect, `pgxpool` не выдал соединение, `codes.Unavailable` от апстрима. |

### Свойства кодов

- **Retryable (клиенту можно повторить):** `TIMEOUT`, `UNAVAILABLE`, `CONFLICT` (после изменения состояния).
- **Не retryable:** `INVALID_ARGUMENT`, `INVALID_REQUEST_BODY`, `VALIDATION_FAILED`, `NOT_FOUND`,
  `ALREADY_EXISTS`, `UNAUTHENTICATED`, `INVALID_CREDENTIALS`, `FORBIDDEN` — повтор того же запроса
  даст тот же результат.
- **`INTERNAL`** — retry на усмотрение клиента, обычно с backoff и ограничением попыток.
- **Наружу без деталей:** `INTERNAL`, `UNAVAILABLE`, `TIMEOUT` — `Public()` всегда отдаёт
  generic-текст, `Message` игнорируется, чтобы не утекли внутренние подробности.
  Остальные коды отдают `Message`, заданный разработчиком (он обязан быть безопасным).

---

## 2. gRPC codes (`google.golang.org/grpc/codes`)

Полный список кодов gRPC и их смысл. Жирным — те, что реально порождает наш маппинг.

| Код | Число | Значение |
|---|---|---|
| `OK` | 0 | Успех. Не ошибка. |
| `Canceled` | 1 | Операция отменена вызывающей стороной (обычно клиент закрыл соединение / отменил контекст). |
| `Unknown` | 2 | Неизвестная ошибка. Например, из другого адресного пространства прилетел статус без кода, или сервер вернул ошибку без деталей. |
| **`InvalidArgument`** | 3 | Клиент передал невалидный аргумент. Не зависит от состояния системы (в отличие от `FailedPrecondition`). Проблема в самих данных запроса. |
| **`DeadlineExceeded`** | 4 | Истёк дедлайн до завершения операции. Может вернуться, даже если операция на сервере успела выполниться. |
| **`NotFound`** | 5 | Запрошенная сущность не найдена. |
| **`AlreadyExists`** | 6 | Сущность, которую клиент пытался создать, уже существует. |
| **`PermissionDenied`** | 7 | У вызывающего нет прав на операцию. Не про аутентификацию (для этого `Unauthenticated`) и не про исчерпание квоты (`ResourceExhausted`). Идентичность известна, прав нет. |
| `ResourceExhausted` | 8 | Исчерпан ресурс: квота, лимит запросов, место на диске. |
| `FailedPrecondition` | 9 | Операция отклонена, потому что система не в том состоянии. В отличие от `Aborted`, повтор без изменения состояния системы не поможет. Пример: удаление непустой директории. |
| `Aborted` | 10 | Операция прервана из-за конфликта конкурентного доступа: неудачная транзакция, потеря оптимистической блокировки. Клиенту обычно стоит повторить всю последовательность (read-modify-write). |
| `OutOfRange` | 11 | Операция вышла за допустимый диапазон (например, seek за пределы файла). В отличие от `InvalidArgument`, этот код указывает на проблему, которая пройдёт при изменении состояния системы. |
| `Unimplemented` | 12 | Операция не реализована / не поддерживается на этом сервере. |
| **`Internal`** | 13 | Внутренняя ошибка. Сломаны инварианты, на которые рассчитывает система. Зарезервировано под серьёзные ошибки. |
| **`Unavailable`** | 14 | Сервис сейчас недоступен. Обычно временно — клиент может повторить с backoff. Не всякая неидемпотентная операция безопасна для повтора. |
| `DataLoss` | 15 | Невосстановимая потеря или повреждение данных. |
| **`Unauthenticated`** | 16 | Запрос не содержит валидных учётных данных для операции. |

Коды `Unknown`, `ResourceExhausted`, `FailedPrecondition`, `OutOfRange`, `Unimplemented`,
`DataLoss` наш прямой маппинг не порождает — при необходимости они появятся с новыми
доменными кодами (например, `RATE_LIMITED → ResourceExhausted`).

---

## 3. HTTP статусы

Значение статусов, которые отдаёт `Code.HTTP()`.

| Статус | Имя | Значение |
|---|---|---|
| `400` | Bad Request | Сервер не может обработать запрос из-за ошибки клиента: битый синтаксис, невалидное тело, неверные параметры. Повтор без изменений бессмыслен. |
| `401` | Unauthorized | Точнее — «Unauthenticated». Нет валидной аутентификации. Клиент может повторить с корректными учётными данными. Ответ по спецификации должен нести `WWW-Authenticate`. |
| `403` | Forbidden | Сервер понял запрос, но отказывает в доступе. Аутентификация не поможет — прав нет. Повтор бессмысленен. |
| `404` | Not Found | Ресурс не найден. Также используется, когда сервер не хочет раскрывать существование ресурса (вместо `403`). |
| `409` | Conflict | Запрос конфликтует с текущим состоянием ресурса: нарушение уникальности, конкурентное изменение, конфликт версий. Клиент может разрешить конфликт и повторить. |
| `500` | Internal Server Error | Непредвиденная ошибка на сервере. Клиенту не сообщаются детали. |
| `503` | Service Unavailable | Сервер временно не может обработать запрос: перегрузка, недоступная зависимость, обслуживание. Можно повторить позже (по возможности с `Retry-After`). |
| `504` | Gateway Timeout | Сервер, выступая шлюзом/прокси, не дождался ответа от апстрима в отведённый срок. В нашем случае — превышен дедлайн вызова зависимости (БД, соседний сервис). |

Почему `TIMEOUT → 504`, а не `408 Request Timeout`: `408` означает, что *клиент* слишком
медленно слал запрос и сервер закрыл простаивающее соединение. У нас же дедлайн истекает
на стороне сервера при обращении к нижестоящей зависимости — это семантика шлюза, `504`.

---

## 4. Сводная таблица маппинга

`apperr.Code` → gRPC (`Code.GRPC()`), HTTP (`Code.HTTP()`), уровень лога (`Code.Level()`).

| `apperr.Code` | gRPC code | HTTP | slog level | Retryable |
|---|---|---|---|---|
| `INTERNAL` | `Internal` (13) | `500` | `ERROR` | на усмотрение клиента |
| `INVALID_ARGUMENT` | `InvalidArgument` (3) | `400` | `WARN` | нет |
| `INVALID_REQUEST_BODY` | `InvalidArgument` (3) | `400` | `WARN` | нет |
| `VALIDATION_FAILED` | `InvalidArgument` (3) | `400` | `WARN` | нет |
| `NOT_FOUND` | `NotFound` (5) | `404` | `WARN` | нет |
| `ALREADY_EXISTS` | `AlreadyExists` (6) | `409` | `WARN` | нет |
| `CONFLICT` | `Aborted` (10) | `409` | `WARN` | да, после изменения состояния |
| `UNAUTHENTICATED` | `Unauthenticated` (16) | `401` | `WARN` | да, с валидными данными |
| `INVALID_CREDENTIALS` | `Unauthenticated` (16) | `401` | `WARN` | да, с валидными данными |
| `FORBIDDEN` | `PermissionDenied` (7) | `403` | `WARN` | нет |
| `TIMEOUT` | `DeadlineExceeded` (4) | `504` | `ERROR` | да, с backoff |
| `UNAVAILABLE` | `Unavailable` (14) | `503` | `ERROR` | да, с backoff |
| *(любой неизвестный)* | `Internal` (13) | `500` | `ERROR` | — |

---

## 5. Обратный маппинг: gRPC code → `apperr.Code`

Используется в gateway (`grpcerr.FromStatus`), когда HTTP-слой получает ответ от gRPC-сервиса.
Маппинг лоссовый: несколько gRPC-кодов схлопываются в один доменный, а часть — в `INTERNAL`.
Точный доменный код восстанавливается из `errdetails.ErrorInfo.Reason`, если апстрим его положил.

| gRPC code | `apperr.Code` |
|---|---|
| `NotFound` (5) | `NOT_FOUND` |
| `AlreadyExists` (6) | `ALREADY_EXISTS` |
| `InvalidArgument` (3) | `INVALID_ARGUMENT` |
| `Unauthenticated` (16) | `UNAUTHENTICATED` |
| `PermissionDenied` (7) | `FORBIDDEN` |
| `Aborted` (10) | `CONFLICT` |
| `DeadlineExceeded` (4) | `TIMEOUT` |
| `Canceled` (1) | `TIMEOUT` |
| `Unavailable` (14) | `UNAVAILABLE` |
| `OK` (0) | *(не ошибка, `nil`)* |
| всё остальное (`Unknown`, `Internal`, `ResourceExhausted`, `FailedPrecondition`, `OutOfRange`, `Unimplemented`, `DataLoss`) | `INTERNAL` |

---

## 6. Обратный маппинг: Postgres → `apperr.Code`

Используется в репозиториях (`pgerr.Map`). Коды — SQLSTATE из `github.com/jackc/pgerrcode`.

| Условие | SQLSTATE | `apperr.Code` |
|---|---|---|
| `errors.Is(err, pgx.ErrNoRows)` | — | `NOT_FOUND` |
| `UNIQUE_VIOLATION` | `23505` | `ALREADY_EXISTS` |
| `FOREIGN_KEY_VIOLATION` | `23503` | `INVALID_ARGUMENT` |
| `CHECK_VIOLATION` | `23514` | `INVALID_ARGUMENT` |
| `NOT_NULL_VIOLATION` | `23502` | `INVALID_ARGUMENT` |
| `SERIALIZATION_FAILURE` | `40001` | `UNAVAILABLE` *(или `CONFLICT` — см. ниже)* |
| `DEADLOCK_DETECTED` | `40P01` | `UNAVAILABLE` *(или `CONFLICT`)* |
| `QUERY_CANCELED` | `57014` | `TIMEOUT` |
| всё остальное | — | `INTERNAL` |

`SERIALIZATION_FAILURE` / `DEADLOCK_DETECTED`: если в сервисе есть внешний retry-цикл
транзакции — логичнее `CONFLICT` (`Aborted`), сигнализируя «повтори всю транзакцию».
Если ретраев нет и клиент просто должен попробовать позже — `UNAVAILABLE`.
Значение по умолчанию в `pgerr.Map` — `UNAVAILABLE`; переопределяется на уровне сервиса.

---

## 7. Формат ответа клиенту

HTTP (`httperr.Write`):

```json
{
  "error": {
    "code": "NOT_FOUND",
    "message": "order 42 not found",
    "request_id": "0f9b7c2e-..."
  }
}
```

Для `VALIDATION_FAILED` добавляется `violations`:

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "message": "validation failed",
    "violations": [
      { "field": "email", "message": "must be a valid email" },
      { "field": "age", "message": "must be >= 18" }
    ],
    "request_id": "0f9b7c2e-..."
  }
}
```

gRPC: `status.New(code.GRPC(), err.Public())` + деталь `errdetails.ErrorInfo{ Reason: "<CODE>", Domain: "delivery" }`.

В лог при этом уходит полная цепочка причин (`error.code`, `error.cause`) на уровне `Code.Level()`.

---

## 8. Пакет `apperr` — ядро

Зависимости ядра: только stdlib + `google.golang.org/grpc/codes` (лист-пакет констант).
Всё, что требует `status`, `pgx`, `net/http`, вынесено в под-пакеты (раздел 9).

### 8.1 `platform/apperr/code.go`

```go
package apperr

import (
	"log/slog"
	"net/http"

	"google.golang.org/grpc/codes"
)

type Code string

const (
	CodeInternal           Code = "INTERNAL"
	CodeInvalidArgument    Code = "INVALID_ARGUMENT"
	CodeInvalidRequestBody Code = "INVALID_REQUEST_BODY"
	CodeValidation         Code = "VALIDATION_FAILED"
	CodeNotFound           Code = "NOT_FOUND"
	CodeAlreadyExists      Code = "ALREADY_EXISTS"
	CodeConflict           Code = "CONFLICT"
	CodeUnauthenticated    Code = "UNAUTHENTICATED"
	CodeInvalidCredentials Code = "INVALID_CREDENTIALS" //nolint:gosec // имя кода, не секрет
	CodeForbidden          Code = "FORBIDDEN"
	CodeTimeout            Code = "TIMEOUT"
	CodeUnavailable        Code = "UNAVAILABLE"
)

const (
	MessageInternalServerError = "internal server error"
	MessageInvalidRequestBody  = "invalid request body"
	MessageValidationFailed    = "validation failed"
	MessageNotFound            = "not found"
	MessageAlreadyExists       = "already exists"
	MessageConflict            = "conflict"
	MessageInvalidArgument     = "invalid argument"
	MessageUnauthenticated     = "unauthenticated"
	MessageInvalidCredentials  = "invalid credentials" //nolint:gosec // текст ошибки, не секрет
	MessageForbidden           = "forbidden"
	MessageTimeout             = "request timeout"
	MessageUnavailable         = "service unavailable"
)

// GRPC — доменный код -> gRPC-статус. Маппинг живёт рядом с Code,
// транспортный пакет не знает про доменные коды.
func (c Code) GRPC() codes.Code {
	switch c {
	case CodeNotFound:
		return codes.NotFound
	case CodeAlreadyExists:
		return codes.AlreadyExists
	case CodeInvalidArgument, CodeInvalidRequestBody, CodeValidation:
		return codes.InvalidArgument
	case CodeConflict:
		return codes.Aborted
	case CodeUnauthenticated, CodeInvalidCredentials:
		return codes.Unauthenticated
	case CodeForbidden:
		return codes.PermissionDenied
	case CodeTimeout:
		return codes.DeadlineExceeded
	case CodeUnavailable:
		return codes.Unavailable
	default:
		return codes.Internal
	}
}

// HTTP — доменный код -> HTTP-статус.
func (c Code) HTTP() int {
	switch c {
	case CodeNotFound:
		return http.StatusNotFound
	case CodeAlreadyExists, CodeConflict:
		return http.StatusConflict
	case CodeInvalidArgument, CodeInvalidRequestBody, CodeValidation:
		return http.StatusBadRequest
	case CodeUnauthenticated, CodeInvalidCredentials:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeTimeout:
		return http.StatusGatewayTimeout
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// Level — уровень лога для этого кода. Interceptor и HTTP-writer
// логируют единообразно, не решая уровень на каждом вызове.
func (c Code) Level() slog.Level {
	switch c {
	case CodeInternal, CodeUnavailable, CodeTimeout:
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}

// Public — дефолтный безопасный текст, если разработчик не задал свой.
func (c Code) Public() string {
	switch c {
	case CodeInvalidArgument:
		return MessageInvalidArgument
	case CodeInvalidRequestBody:
		return MessageInvalidRequestBody
	case CodeValidation:
		return MessageValidationFailed
	case CodeNotFound:
		return MessageNotFound
	case CodeAlreadyExists:
		return MessageAlreadyExists
	case CodeConflict:
		return MessageConflict
	case CodeUnauthenticated:
		return MessageUnauthenticated
	case CodeInvalidCredentials:
		return MessageInvalidCredentials
	case CodeForbidden:
		return MessageForbidden
	case CodeTimeout:
		return MessageTimeout
	case CodeUnavailable:
		return MessageUnavailable
	default:
		return MessageInternalServerError
	}
}

// CodeFromGRPC — обратный (лоссовый) маппинг для gateway.
func CodeFromGRPC(c codes.Code) Code {
	switch c {
	case codes.OK:
		return ""
	case codes.NotFound:
		return CodeNotFound
	case codes.AlreadyExists:
		return CodeAlreadyExists
	case codes.InvalidArgument:
		return CodeInvalidArgument
	case codes.Unauthenticated:
		return CodeUnauthenticated
	case codes.PermissionDenied:
		return CodeForbidden
	case codes.Aborted:
		return CodeConflict
	case codes.DeadlineExceeded, codes.Canceled:
		return CodeTimeout
	case codes.Unavailable:
		return CodeUnavailable
	default:
		return CodeInternal
	}
}
```

### 8.2 `platform/apperr/error.go`

```go
package apperr

import (
	"errors"
	"log/slog"
)

// Error — доменная ошибка, единая для всех слоёв.
type Error struct {
	Code       Code        // машинный код, драйвит маппинг в транспорт
	Message    string      // безопасный текст для клиента
	Err        error       // внутренняя причина, только в логи
	Violations []Violation // безопасные детали полей, опционально
}

// Violation — ошибка конкретного поля, безопасна для отдачи клиенту.
type Violation struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	switch {
	case e.Err != nil && e.Message != "":
		return e.Message + ": " + e.Err.Error()
	case e.Err != nil:
		return e.Err.Error()
	default:
		return e.Message
	}
}

func (e *Error) Unwrap() error { return e.Err }

// Is — сравнение по коду: errors.Is(err, apperr.ErrNotFound).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// Public — текст, который реально уходит клиенту.
// Для внутренних кодов Message игнорируется, отдаётся generic-текст.
func (e *Error) Public() string {
	switch e.Code {
	case CodeInternal, CodeUnavailable, CodeTimeout:
		return e.Code.Public()
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Code.Public()
}

// LogValue — структурные поля для slog без утечки клиенту.
// logger.Err(err) уже делает slog.Any("error", err) — этого достаточно.
func (e *Error) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("code", string(e.Code)),
		slog.String("public_message", e.Public()),
	}
	if e.Err != nil {
		attrs = append(attrs, slog.String("cause", e.Err.Error()))
	}
	if len(e.Violations) > 0 {
		attrs = append(attrs, slog.Int("violations", len(e.Violations)))
	}
	return slog.GroupValue(attrs...)
}

// Sentinels для errors.Is (сравнение по коду через метод Is).
var (
	ErrInternal        = &Error{Code: CodeInternal}
	ErrInvalidArgument = &Error{Code: CodeInvalidArgument}
	ErrNotFound        = &Error{Code: CodeNotFound}
	ErrAlreadyExists   = &Error{Code: CodeAlreadyExists}
	ErrConflict        = &Error{Code: CodeConflict}
	ErrUnauthenticated = &Error{Code: CodeUnauthenticated}
	ErrForbidden       = &Error{Code: CodeForbidden}
	ErrTimeout         = &Error{Code: CodeTimeout}
	ErrUnavailable     = &Error{Code: CodeUnavailable}
)

// As — извлечь *Error из цепочки.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// From — нормализовать ЛЮБУЮ ошибку в *Error (fallback — INTERNAL).
func From(err error) *Error {
	if err == nil {
		return nil
	}
	if e, ok := As(err); ok {
		return e
	}
	return &Error{Code: CodeInternal, Err: err}
}
```

### 8.3 `platform/apperr/constructors.go`

```go
package apperr

import "fmt"

func New(code Code, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

// Wrap — прикрепить внутреннюю причину, сохранив код и Message. Чейнится.
func (e *Error) Wrap(err error) *Error {
	e.Err = err
	return e
}

// WithViolations — добавить детали полей. Чейнится.
func (e *Error) WithViolations(v ...Violation) *Error {
	e.Violations = append(e.Violations, v...)
	return e
}

// Сахар по кодам: короткая и форматная версии.
func Internal(msg string) *Error          { return New(CodeInternal, msg) }
func Internalf(f string, a ...any) *Error { return New(CodeInternal, fmt.Sprintf(f, a...)) }

func InvalidArgument(msg string) *Error          { return New(CodeInvalidArgument, msg) }
func InvalidArgumentf(f string, a ...any) *Error { return New(CodeInvalidArgument, fmt.Sprintf(f, a...)) }

func InvalidRequestBody(msg string) *Error { return New(CodeInvalidRequestBody, msg) }

func NotFound(msg string) *Error          { return New(CodeNotFound, msg) }
func NotFoundf(f string, a ...any) *Error { return New(CodeNotFound, fmt.Sprintf(f, a...)) }

func AlreadyExists(msg string) *Error          { return New(CodeAlreadyExists, msg) }
func AlreadyExistsf(f string, a ...any) *Error { return New(CodeAlreadyExists, fmt.Sprintf(f, a...)) }

func Conflict(msg string) *Error          { return New(CodeConflict, msg) }
func Conflictf(f string, a ...any) *Error { return New(CodeConflict, fmt.Sprintf(f, a...)) }

func Unauthenticated(msg string) *Error { return New(CodeUnauthenticated, msg) }
func InvalidCredentials() *Error        { return New(CodeInvalidCredentials, MessageInvalidCredentials) }

func Forbidden(msg string) *Error          { return New(CodeForbidden, msg) }
func Forbiddenf(f string, a ...any) *Error { return New(CodeForbidden, fmt.Sprintf(f, a...)) }

func Timeout(msg string) *Error     { return New(CodeTimeout, msg) }
func Unavailable(msg string) *Error { return New(CodeUnavailable, msg) }

// Validation — составная ошибка валидации со списком полей.
func Validation(v ...Violation) *Error {
	return (&Error{Code: CodeValidation, Message: MessageValidationFailed}).WithViolations(v...)
}
```

---

## 9. Адаптеры

### 9.1 `platform/apperr/pgerr/pgerr.go`

Импортирует `pgx` — изолирован от ядра.

```go
package pgerr

import (
	"errors"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

// Map — ошибка драйвера/БД -> *apperr.Error. Вызывается в репозитории.
func Map(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := apperr.As(err); ok {
		return err // уже доменная — не перетираем
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound(apperr.MessageNotFound).Wrap(err)
	}

	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case pgerrcode.UniqueViolation:
			return apperr.AlreadyExists(apperr.MessageAlreadyExists).Wrap(err)
		case pgerrcode.ForeignKeyViolation,
			pgerrcode.CheckViolation,
			pgerrcode.NotNullViolation:
			return apperr.InvalidArgument(apperr.MessageInvalidArgument).Wrap(err)
		case pgerrcode.SerializationFailure, pgerrcode.DeadlockDetected:
			return apperr.Unavailable(apperr.MessageUnavailable).Wrap(err)
		case pgerrcode.QueryCanceled:
			return apperr.Timeout(apperr.MessageTimeout).Wrap(err)
		}
	}
	return apperr.Internal(apperr.MessageInternalServerError).Wrap(err)
}
```

Репозиторий:

```go
func (r *OrderRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Order, error) {
	row := r.pool.QueryRow(ctx, `SELECT ... FROM orders WHERE id = $1`, id)

	var o domain.Order
	if err := row.Scan(&o.ID, &o.UserID, &o.Total); err != nil {
		return nil, pgerr.Map(err) // pgx.ErrNoRows -> apperr NOT_FOUND
	}
	return &o, nil
}
```

### 9.2 `platform/apperr/grpcerr/grpcerr.go`

Импортирует `status` и `errdetails`.

```go
package grpcerr

import (
	"context"
	"log/slog"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

const domain = "delivery"

// UnaryServerInterceptor — единая точка перевода доменной ошибки в gRPC-статус
// на стороне сервиса (user, order). Ставится через grpc.ChainUnaryInterceptor.
func UnaryServerInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}

		e := apperr.From(err)

		log.LogAttrs(ctx, e.Code.Level(), "grpc handler failed",
			slog.String("method", info.FullMethod),
			slog.Any("error", e), // LogValue: code + cause в логах
		)

		st := status.New(e.Code.GRPC(), e.Public()) // в сеть — только Public()

		// protoadapt.MessageV1 == proto.Message; errdetails.* его реализуют.
		details := []protoadapt.MessageV1{
			&errdetails.ErrorInfo{Reason: string(e.Code), Domain: domain},
		}
		if len(e.Violations) > 0 {
			br := &errdetails.BadRequest{}
			for _, v := range e.Violations {
				br.FieldViolations = append(br.FieldViolations,
					&errdetails.BadRequest_FieldViolation{Field: v.Field, Description: v.Message})
			}
			details = append(details, br)
		}
		if enriched, derr := st.WithDetails(details...); derr == nil {
			st = enriched
		}
		return nil, st.Err()
	}
}

// FromStatus — обратный перевод на стороне клиента (gateway).
func FromStatus(err error) *apperr.Error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return apperr.Internal(apperr.MessageInternalServerError).Wrap(err)
	}

	code := apperr.CodeFromGRPC(st.Code())
	var violations []apperr.Violation

	for _, d := range st.Details() {
		switch t := d.(type) {
		case *errdetails.ErrorInfo:
			if t.GetReason() != "" && t.GetDomain() == domain {
				code = apperr.Code(t.GetReason()) // точный доменный код
			}
		case *errdetails.BadRequest:
			for _, fv := range t.GetFieldViolations() {
				violations = append(violations,
					apperr.Violation{Field: fv.GetField(), Message: fv.GetDescription()})
			}
		}
	}

	out := apperr.New(code, st.Message()).Wrap(err)
	if len(violations) > 0 {
		out.WithViolations(violations...)
	}
	return out
}
```

### 9.3 `platform/apperr/httperr/httperr.go`

Импортирует `net/http` + `logger`/`appctx`.

```go
package httperr

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

type responseBody struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code       string             `json:"code"`
	Message    string             `json:"message"`
	Violations []apperr.Violation `json:"violations,omitempty"`
	RequestID  string             `json:"request_id,omitempty"`
}

// Write — единая точка отдачи ошибки в HTTP: статус + JSON + лог с причиной.
func Write(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	e := apperr.From(err)

	log.LogAttrs(r.Context(), e.Code.Level(), "http handler failed",
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Any("error", e),
	)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(e.Code.HTTP())
	_ = json.NewEncoder(w).Encode(responseBody{Error: errorBody{
		Code:       string(e.Code),
		Message:    e.Public(),
		Violations: e.Violations,
		RequestID:  appctx.GetRequestID(r.Context()),
	}})
}
```

---

## 10. Сквозной пример: от Postgres до клиента

Путь вызова `GetUser` по всем слоям user-сервиса и gateway, в порядке файлов
`domain → repository/postgres → usecase → transport/grpc → transport/http`.

Правило: `*apperr.Error` создаётся один раз (в репозитории через `pgerr.Map`),
по пути обрастает понятным сообщением, а переводится в транспорт и логируется
ровно один раз — на внешней границе (interceptor в сервисе, `httperr.Write` в gateway).
Внутренние слои не логируют.

### 10.1 `domain/errors.go`

Сам тип и коды — это пакет `platform/apperr` (§8). В домене сервиса ничего дублировать
не нужно, только импортировать `apperr` и, при желании, объявить свои sentinel-обёртки.

### 10.2 `domain/user.go` — сущность, порт репозитория, инварианты

```go
package domain

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

type Status string

const (
	StatusActive  Status = "ACTIVE"
	StatusBlocked Status = "BLOCKED"
)

type User struct {
	ID     uuid.UUID
	Email  string
	Status Status
}

// Порт: реализуется в слое инфраструктуры.
type UserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)
	Create(ctx context.Context, u User) error
}

// Инварианты сущности домен проверяет сам и возвращает apperr напрямую
// (apperr — лист-пакет платформы, тяжёлых зависимостей не тянет).
func NewUser(email string) (*User, error) {
	if !strings.Contains(email, "@") {
		return nil, apperr.InvalidArgument("email must be a valid address")
	}
	return &User{ID: uuid.New(), Email: email, Status: StatusActive}, nil
}
```

### 10.3 `repository/postgres/user.go` — единственная работа с ошибкой: `pgerr.Map`

```go
package postgres

func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	const q = `SELECT id, email, status FROM users WHERE id = $1`

	var u domain.User
	err := r.pool.QueryRow(ctx, q, id).Scan(&u.ID, &u.Email, &u.Status)
	if err != nil {
		return nil, pgerr.Map(err)
		// pgx.ErrNoRows  -> *apperr.Error{Code: NOT_FOUND,      Err: "no rows in result set"}
		// SQLSTATE 23505 -> *apperr.Error{Code: ALREADY_EXISTS, Err: *pgconn.PgError}
		// прочее         -> *apperr.Error{Code: INTERNAL,       Err: <оригинал>}
	}
	return &u, nil
}

func (r *UserRepo) Create(ctx context.Context, u domain.User) error {
	const q = `INSERT INTO users (id, email, status) VALUES ($1, $2, $3)`
	_, err := r.pool.Exec(ctx, q, u.ID, u.Email, u.Status)
	return pgerr.Map(err) // nil -> nil
}
```

Репозиторий **не** логирует, **не** оборачивает в `fmt.Errorf` без `apperr` (иначе на
границе код потеряется и станет `INTERNAL`), **не** решает, что уникальный индекс — это
«email занят»: это знает usecase, у него есть контекст операции.

### 10.4 `usecase/user.go` — бизнес-смысл: сообщение и, при необходимости, другой код

```go
package usecase

func (uc *UserUseCase) GetUser(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	u, err := uc.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// код тот же (NOT_FOUND), но сообщение — человекочитаемое, с id
			return nil, apperr.NotFoundf("user %s not found", id).Wrap(err)
		}
		return nil, err // остальные коды пробрасываем как есть
	}
	return u, nil
}

func (uc *UserUseCase) Register(ctx context.Context, email string) (*domain.User, error) {
	u, err := domain.NewUser(email) // domain -> apperr.InvalidArgument при плохом email
	if err != nil {
		return nil, err
	}
	if err := uc.repo.Create(ctx, *u); err != nil {
		if errors.Is(err, apperr.ErrAlreadyExists) {
			// ALREADY_EXISTS из репозитория превращаем в понятную клиенту причину
			return nil, apperr.AlreadyExistsf("email %s is already taken", email).Wrap(err)
		}
		return nil, err
	}
	return u, nil
}
```

usecase тоже **не** логирует — иначе одна ошибка попадёт в лог дважды.

### 10.5 `transport/grpc` — interceptor (перевод + единственный лог) и чистый handler

Тело interceptor — в §9.2 (`grpcerr.UnaryServerInterceptor`). Подключение в `grpcserver`
(нужна опция `WithServerOptions` — поле `serverOpts` в `grpcserver.Server` уже есть,
осталось добавить сеттер):

```go
// services/user/internal/app/app.go
grpcSrv := grpcserver.New(
	grpcserver.WithPort(cfg.GRPC.Port),
	grpcserver.WithServerOptions(
		grpc.ChainUnaryInterceptor(
			requestid.UnaryServerInterceptor(),   // кладёт request_id в ctx
			grpcerr.UnaryServerInterceptor(log),  // последним: ловит ошибку хендлера
		),
	),
)
```

```go
// services/user/internal/delivery/grpc/user.go
func (h *UserHandler) GetUser(
	ctx context.Context, req *userv1.GetUserRequest,
) (*userv1.GetUserResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, apperr.InvalidArgument("id must be a valid uuid").Wrap(err)
	}

	u, err := h.uc.GetUser(ctx, id)
	if err != nil {
		return nil, err // не трогаем — переведёт и залогирует interceptor
	}
	return &userv1.GetUserResponse{User: toProto(u)}, nil
}
```

При отсутствии пользователя interceptor даёт:

- **лог user-сервиса:**
  ```
  level=WARN msg="grpc handler failed"
    method=/user.v1.UserService/GetUser request_id=0f9b7c2e-…
    error.code=NOT_FOUND
    error.public_message="user 42 not found"
    error.cause="user 42 not found: no rows in result set"
  ```
- **в сеть:** `status.Code = NotFound`, `message = "user 42 not found"`,
  деталь `ErrorInfo{ Reason: "NOT_FOUND", Domain: "delivery" }`.

### 10.6 `transport/http` — writer (§9.3) и handler gateway

`grpcerr.FromStatus` разворачивает ответ gRPC обратно в `*apperr.Error`,
`httperr.Write` отдаёт статус + JSON и делает единственный лог на стороне gateway.

```go
// services/gateway/internal/delivery/http/user.go

// GET /users/{id}
func (h *UserHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	resp, err := h.userClient.GetUser(r.Context(), &userv1.GetUserRequest{Id: id})
	if err != nil {
		httperr.Write(w, r, h.log, grpcerr.FromStatus(err))
		// FromStatus:    status NotFound + ErrorInfo -> *apperr.Error{Code: NOT_FOUND, Message: "user 42 not found"}
		// httperr.Write: статус 404, JSON клиенту, лог WARN с cause
		return
	}
	response.OK(w, r, fromProto(resp.GetUser()))
}

// POST /users — свой разбор тела запроса
func (h *UserHandler) Register(w http.ResponseWriter, r *http.Request) {
	var body registerRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httperr.Write(w, r, h.log,
			apperr.InvalidRequestBody(apperr.MessageInvalidRequestBody).Wrap(err))
		return
	}

	resp, err := h.userClient.Register(r.Context(), body.toProto())
	if err != nil {
		httperr.Write(w, r, h.log, grpcerr.FromStatus(err))
		return
	}
	response.Created(w, r, fromProto(resp.GetUser()))
}
```

### 10.7 Что получает каждый конец

| Точка | Код / статус | Детали |
|---|---|---|
| repository | `*apperr.Error{NOT_FOUND}` | `Err = pgx.ErrNoRows` |
| usecase | `*apperr.Error{NOT_FOUND}` | `Message = "user 42 not found"`, `Err` — цепочка |
| user-сервис, лог | `WARN` | `error.code=NOT_FOUND`, `error.cause="…: no rows in result set"` |
| user-сервис, сеть | `codes.NotFound` | `message="user 42 not found"` + `ErrorInfo{NOT_FOUND}` |
| gateway, лог | `WARN` | тот же `error.cause`, плюс `path=/users/42` |
| gateway → клиент | `HTTP 404` | `{"error":{"code":"NOT_FOUND","message":"user 42 not found","request_id":"…"}}` |

### 10.8 Валидация нескольких полей

```go
func (uc *UserUseCase) Register(ctx context.Context, in RegisterInput) error {
	var v []apperr.Violation
	if !validEmail(in.Email) {
		v = append(v, apperr.Violation{Field: "email", Message: "must be a valid email"})
	}
	if in.Age < 18 {
		v = append(v, apperr.Violation{Field: "age", Message: "must be >= 18"})
	}
	if len(v) > 0 {
		return apperr.Validation(v...) // HTTP 400 / codes.InvalidArgument + BadRequest details
	}
	// ...
}
```
