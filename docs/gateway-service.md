# Gateway Service — HTTP-шлюз к order-service

Практический документ по образцу [order-service.md](order-service.md): реальная
(плоская) раскладка пакетов gateway-сервиса, как в текущем коде `order`/`user`, а не
aspirational-структура из [architecture.md](architecture.md) (`ports/`,
`adapters/inbound|outbound`).

MVP этого документа — **одна ручка**: `POST /v1/orders`, HTTP → gRPC `order.CreateOrder`.
Остальное (`GET /v1/orders/{id}`, проксирование к `user`/`product`) добавляется тем же
паттерном позже, см. §17.

Опирается на:

- [architecture.md](architecture.md) §4.2, §6.8, §8 — HTTP-контракт, место gateway в
  Clean Architecture, fake-auth middleware.
- [errors.md](errors.md) — коды `apperr`, их маппинг в gRPC/HTTP; здесь **меняется
  только форма JSON-тела** (добавляется конверт `meta`/`data`/`error`), таблицы кодов
  и HTTP-статусов из errors.md не трогаются.
- [db-mapping.md](db-mapping.md) — здесь неприменим (у gateway нет БД), упомянут
  только для полноты списка.

---

## 1. Что делает gateway

```
client → HTTP POST /v1/orders → gateway → gRPC order.CreateOrder → order-service
```

- **Аутентификация.** Пока fake-auth (см. [architecture.md](architecture.md) §8):
  `user_id` берётся из заголовка `X-User-Id`, а если его нет — из конфига
  `FAKE_AUTH_USER_ID` (тот же seed-UUID, что и в `userdb`). В тело запроса `user_id`
  клиент не передаёт.
- **Тело запроса.** `address_id` + список `{product_id, quantity}` — ровно то, что
  ждёт `order.v1.CreateOrderRequest` (см. `proto/proto/order/v1/order_service.proto`),
  минус `user_id` (он из контекста).
- **Синтаксическая валидация** на границе gateway — `ozzo-validation`, до похода в
  gRPC. Бизнес-валидацию (существование `address_id`/`product_id`, инварианты заказа)
  всё так же делает `order`-service — gateway её не дублирует.
- **Ошибки** приходят от `order`-service как gRPC `status` и разворачиваются обратно
  в `*apperr.Error` через `grpcerr.Map` (уже описано в
  [errors.md](errors.md) §9.2, §10.6) — в gateway новое только то, как эта ошибка
  укладывается в JSON-тело (§2).

---

## 2. Единый формат ответа: `meta` / `data` / `error`

Все HTTP-ответы gateway — один и тот же конверт, независимо от успеха или ошибки:

```go
type Envelope struct {
    Meta  Meta       `json:"meta"`
    Data  any        `json:"data"`
    Error *ErrorBody `json:"error"`
}

type Meta struct {
    RequestID string `json:"request_id,omitempty"`
}

type ErrorBody struct {
    Code       string             `json:"code"`
    Reason     string             `json:"reason,omitempty"`
    Message    string             `json:"message"`
    Violations []apperr.Violation `json:"violations,omitempty"`
}
```

Успех (`201 Created`):

```json
{
  "meta": { "request_id": "0f9b7c2e-..." },
  "data": {
    "id": "0192a0c4-...",
    "user_id": "00000000-0000-7000-8000-000000000001",
    "status": "created",
    "items": [
      { "id": "...", "product_id": "...", "product_name": "Пицца", "quantity": 2, "unit_price": 62500 }
    ],
    "total_amount": 125000,
    "delivery_address": "г. Москва, ...",
    "created_at": "2026-09-14T12:00:00Z",
    "updated_at": "2026-09-14T12:00:00Z"
  },
  "error": null
}
```

Ошибка (`404 Not Found`, продукт не найден):

```json
{
  "meta": { "request_id": "0f9b7c2e-..." },
  "data": null,
  "error": { "code": "NOT_FOUND", "message": "order: product not found" }
}
```

Ошибка составной валидации (`400 Bad Request`):

```json
{
  "meta": { "request_id": "0f9b7c2e-..." },
  "data": null,
  "error": {
    "code": "INVALID_ARGUMENT",
    "reason": "VALIDATION_FAILED",
    "message": "validation failed",
    "violations": [
      { "field": "address_id", "message": "cannot be blank" },
      { "field": "items.0.quantity", "message": "must be no less than 1" }
    ]
  }
}
```

HTTP-статус — всё так же `apperr.Code.HTTP()` из [errors.md](errors.md) §3/§4, меняется
только тело. `code`/`reason`/`violations` — те же значения, что и в §7 errors.md, просто
теперь `data`/`error` — соседние поля одного конверта, а не единственное поле `error`
верхнего уровня. Единственное исключение — `204 No Content` (если появится): у него по
HTTP-спецификации нет тела, конверт туда не пишем.

**Важно:** в errors.md §9.4 есть эскиз `platform/apperr/httperr.Write` с телом
`{"error": {...}}` без `meta`/`data` — этот пакет в коде ещё не создан (проверено:
`httperr.go` не существует, только план). Вместо него ниже заводится
`platform/http/response` — он покрывает и успех, и ошибку одним типом `Envelope`, чтобы
конверт не разъезжался между двумя пакетами. Если `platform/apperr/httperr` всё же
понадобится отдельно — его тело должно повторять этот формат.

---

## 3. Дерево пакетов

```
services/gateway/
  cmd/main.go                       # + аннотации swag @title/@version/@BasePath
  internal/
    app/app.go                      # composition root: conn к order-service → client →
                                    #   usecase → v1-handler → chi router + middlewares
    config/config.go                # + OrderService.Addr, FakeAuth.DefaultUserID
    domain/
      order.go                      # Order/OrderItem — общий тип, а не вывод одного usecase
    usecase/
      order.go                      # CreateOrderInput/Item, порт OrderProvider,
                                    #   OrderUsecaseDeps, OrderUsecase (возвращает domain.Order)
    client/
      order.go                     # OrderClient над orderv1.OrderServiceClient
                                    #   реализует OrderProvider; protoToOrder → domain.Order
    transport/http/
      router.go                    # NewRouter: r.Route("/v1", order.Routes) (+ user.Routes, ... позже)
      v1/
        order/
          dto.go                     # CreateOrderRequestDTO/Item + Validate() (ozzo)
                                      #   OrderResponseDTO/Item
          mapper.go                   # dto → usecase.CreateOrderInput, domain.Order → dto
          handler.go                  # OrderHandler.CreateOrder + swag-аннотации,
                                      #   валидация через platform/apperr/ozzoerr.Map
          router.go                    # Routes(r, h): r.Post("/orders", h.CreateOrder)
        # user/, product/ — та же раскладка позже, тем же паттерном (см. §1)
  internal/docs/                    # сгенерировано swag init (docs.go, swagger.json/yaml)
```

`domain` заводим, но тонкий — без бизнес-инвариантов и без фабрики вида `domain.NewOrder(...)`:
gateway не создаёт заказ и не хранит его инварианты (это по-прежнему ответственность
`order`-service, [architecture.md](architecture.md) §6.8), а только отображает то, что
получил обратно. Причина не в бизнес-правилах, а в том, что `Order`/`OrderItem` — общий
тип для нескольких usecase'ов gateway (`CreateOrder` сейчас, `GetOrder`/`ListOrders`
позже, §18 п.12), а не собственность одного из них — деталь разобрана в §8.
Тот же приём уже используется в `order`-сервисе: `domain.User`/`domain.Address` там тоже
просто зеркало чужого proto, без своих инвариантов (см. [order-service.md](order-service.md) §2).

---

## 4. `platform`: что добавляем

| Пакет | Назначение |
|---|---|
| `platform/http/response` | `Envelope`, `Meta`, `ErrorBody` + `OK`/`Created`/`Fail` (см. §5) — `Fail` только формирует ответ, не логирует |
| `platform/http/bind` | `JSON[T]` — decode тела запроса + валидация (`ozzoerr.Map(T.Validate())`) в один вызов (см. §4.4) |
| `platform/http/middleware` | `RequestID`, `Logger`, `Recover`, `FakeAuth` — chi-middleware; `Logger` — единственное место логирования HTTP-слоя (см. §7) |
| `platform/appctx` | + `UserIDKey`/`WithUserID`/`GetUserID`, `ErrBox`/`WithErrBox`/`SetError` (см. §4.1, §4.2) |
| `platform/apperr/ozzoerr` | `Flatten`/`Map` — ozzo-validation → `[]apperr.Violation` (см. §4.3), общий для всех `v1/<entity>` |

### 4.1 `platform/appctx/user_id.go`

```go
package appctx

import "context"

const UserIDKey ctxKey = "user_id"

func WithUserID(ctx context.Context, id string) context.Context {
    return context.WithValue(ctx, UserIDKey, id)
}

func GetUserID(ctx context.Context) string {
    if ctx == nil {
        return ""
    }
    if id, ok := ctx.Value(UserIDKey).(string); ok {
        return id
    }
    return ""
}
```

### 4.2 `platform/appctx/error_box.go`

`ErrBox` — общий контейнер: `response.Fail` (§5) кладёт в него ошибку, а
`middleware.Logger` (§7) после `next.ServeHTTP` её оттуда читает и логирует. Нужен,
потому что `http.Handler` ничего не возвращает — в отличие от gRPC-интерсепторов
(`platform/grpc/interceptors`), где `interceptors.Error()`/`interceptors.Logger()`
общаются через возврат `error` из `handler(ctx, req)`. `ErrBox`, положенный в
контекст указателем, заменяет этот канал (тот же приём, что у
`chimw.WrapResponseWriter` для статус-кода).

```go
package appctx

import "context"

const errBoxKey ctxKey = "err_box"

type ErrBox struct {
    Err error
}

func WithErrBox(ctx context.Context) (context.Context, *ErrBox) {
    box := &ErrBox{}
    return context.WithValue(ctx, errBoxKey, box), box
}

func SetError(ctx context.Context, err error) {
    if box, ok := ctx.Value(errBoxKey).(*ErrBox); ok {
        box.Err = err
    }
}
```

### 4.3 `platform/apperr/ozzoerr/ozzoerr.go`

Тот же приём, что `grpcerr`/`pgerr` (`platform/apperr/grpcerr`, `platform/apperr/pgerr`) —
адаптер конкретной библиотеки к `apperr`, только для `ozzo-validation`. Общий для
всех `v1/<entity>` пакетов gateway — избавляет `v1/order`/`v1/user`/`v1/product` от
дублирования одного и того же `flatten` (см. §11.2).

```go
package ozzoerr

import (
    "sort"

    validation "github.com/go-ozzo/ozzo-validation/v4"

    "github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

// Flatten разворачивает вложенную validation.Errors (в т.ч. по индексам срезов,
// например Items[1].Quantity) в плоский список apperr.Violation с путём вида
// "items.1.quantity" — именно так это отдаётся клиенту (errors.md §7).
func Flatten(prefix string, err error) []apperr.Violation {
    verrs, ok := err.(validation.Errors)
    if !ok {
        return []apperr.Violation{{Field: prefix, Message: err.Error()}}
    }

    keys := make([]string, 0, len(verrs))
    for k := range verrs {
        keys = append(keys, k)
    }
    sort.Strings(keys) // детерминированный порядок в ответе

    var out []apperr.Violation
    for _, k := range keys {
        field := k
        if prefix != "" {
            field = prefix + "." + k
        }
        out = append(out, Flatten(field, verrs[k])...)
    }
    return out
}

// Map — nil остаётся nil, иначе всегда apperr.ValidationFailed(...)
// (platform/apperr/constructors.go: CodeInvalidArgument + Reason=VALIDATION_FAILED).
func Map(err error) error {
    if err == nil {
        return nil
    }
    return apperr.ValidationFailed(Flatten("", err)...)
}
```

### 4.4 `platform/http/bind/bind.go`

Отдельный пакет, а не часть `response` (§5) или самого `v1/order` — намеренно: это
единственное место, где decode JSON-тела и `ozzoerr.Map(dto.Validate())` (§4.3)
объединены в один вызов, чтобы `v1/order/handler.go` (и будущие `v1/user`,
`v1/product`, §1) не повторяли одни и те же пять строк
`json.NewDecoder(...).Decode(...)` + `ozzoerr.Map(dto.Validate())`. `response` (§5)
остаётся только про исходящий конверт — `bind` про входящий разбор тела, смешивать
их в одном пакете было бы тем же нарушением границы, ради которого `ozzoerr` вынесен
из `v1/order` в общий `platform/apperr/ozzoerr` (см. §11.2).

```go
package bind

import (
    "encoding/json"
    "net/http"

    "github.com/maksimegorovdev/delivery-backend/platform/apperr"
    "github.com/maksimegorovdev/delivery-backend/platform/apperr/ozzoerr"
)

// Validatable — контракт для JSON: DTO с ozzo-валидацией, Validate() как в §11.1.
type Validatable interface {
    Validate() error
}

// JSON декодирует тело запроса в T и валидирует его через T.Validate() — ozzo
// validation.Errors разворачивается в *apperr.Error через ozzoerr.Map (§4.3).
// Ошибка decode тоже приходит как *apperr.Error (apperr.InvalidRequestBody) —
// вызывающему коду (handler.go, §11.4) не нужно различать decode/validate,
// обе одинаково уходят в response.Fail.
func JSON[T Validatable](r *http.Request) (T, error) {
    var dto T
    if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
        return dto, apperr.InvalidRequestBody().Wrap(err)
    }
    if err := ozzoerr.Map(dto.Validate()); err != nil {
        return dto, err
    }
    return dto, nil
}
```

---

## 5. `platform/http/response` — конверт

```go
package response

import (
    "context"
    "encoding/json"
    "net/http"

    "github.com/maksimegorovdev/delivery-backend/platform/appctx"
    "github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

type Meta struct {
    RequestID string `json:"request_id,omitempty"`
}

type ErrorBody struct {
    Code       string             `json:"code"`
    Reason     string             `json:"reason,omitempty"`
    Message    string             `json:"message"`
    Violations []apperr.Violation `json:"violations,omitempty"`
}

type Envelope struct {
    Meta  Meta       `json:"meta"`
    Data  any        `json:"data"`
    Error *ErrorBody `json:"error"`
}

func meta(ctx context.Context) Meta {
    return Meta{RequestID: appctx.GetRequestID(ctx)}
}

func write(w http.ResponseWriter, status int, env Envelope) {
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(env)
}

// OK — 200, успех.
func OK(w http.ResponseWriter, r *http.Request, data any) {
    write(w, http.StatusOK, Envelope{Meta: meta(r.Context()), Data: data})
}

// Created — 201, успех создания.
func Created(w http.ResponseWriter, r *http.Request, data any) {
    write(w, http.StatusCreated, Envelope{Meta: meta(r.Context()), Data: data})
}

// Fail — единая точка отдачи ошибки: статус по apperr.Code.HTTP() + JSON-конверт.
// Аналог httperr.Write из errors.md §9.4, но пишет Envelope, а не голое
// {"error": {...}}. Fail НЕ логирует (логи — только в middleware, см. §7): вместо
// этого кладёт ошибку в appctx.ErrBox (appctx.SetError, §4.2), а читает и логирует
// её оттуда единственный логгер HTTP-слоя — middleware.Logger, уже после
// next.ServeHTTP. Тот же принцип, что у platform/grpc/interceptors:
// interceptors.Error() только маппит ошибку в status, interceptors.Logger() —
// единственный логгер.
func Fail(w http.ResponseWriter, r *http.Request, err error) {
    e := apperr.From(err)
    appctx.SetError(r.Context(), e)

    write(w, e.Code.HTTP(), Envelope{
        Meta: meta(r.Context()),
        Error: &ErrorBody{
            Code:       string(e.Code),
            Reason:     e.Reason,
            Message:    e.Public(),
            Violations: e.Violations,
        },
    })
}
```

`Data: nil` в `Fail` даёт `"data": null` в JSON — конверт остаётся одной и той же формы
на успехе и на ошибке, клиенту не нужно ветвиться по набору полей.

---

## 6. Config

```go
// services/gateway/internal/config/config.go
type Config struct {
    App          App
    Log          Log
    HTTP         HTTP
    OrderService OrderService
    FakeAuth     FakeAuth
}

type OrderService struct {
    Addr string `env:"ORDER_SERVICE_GRPC_ADDR,required"`
}

func (o OrderService) Validate() error {
    return validation.ValidateStruct(&o,
        validation.Field(&o.Addr, validation.Required),
    )
}

type FakeAuth struct {
    DefaultUserID string `env:"FAKE_AUTH_USER_ID,required"`
}

func (f FakeAuth) Validate() error {
    return validation.ValidateStruct(&f,
        validation.Field(&f.DefaultUserID, validation.Required, is.UUID),
    )
}
```

`Config.Validate()` дополняется полями `OrderService`, `FakeAuth` тем же способом, что
и `App`/`Log`/`HTTP` — по образцу уже существующего кода (см. `services/gateway/internal/config/config.go`).
`FAKE_AUTH_USER_ID` — тот же UUID, что засеян в `userdb` (см. [architecture.md](architecture.md) §5.1).

---

## 7. `platform/http/middleware` — chi-middleware

Аналог `platform/grpc/interceptors`, но для HTTP-транспорта gateway. Каждый — свой файл.

```go
// platform/http/middleware/request_id.go
package middleware

import (
    "net/http"

    "github.com/google/uuid"

    "github.com/maksimegorovdev/delivery-backend/platform/appctx"
)

// RequestID — если клиент не прислал X-Request-Id, генерирует свой (uuidv7),
// кладёт в appctx (response.Fail/OK читают его оттуда для meta.request_id)
// и отдаёт обратно в заголовке — чтобы клиент мог его увидеть даже при panic
// до первой записи в тело.
func RequestID(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        id := r.Header.Get("X-Request-Id")
        if id == "" {
            id = uuid.Must(uuid.NewV7()).String()
        }
        w.Header().Set("X-Request-Id", id)
        next.ServeHTTP(w, r.WithContext(appctx.WithRequestID(r.Context(), id)))
    })
}
```

```go
// platform/http/middleware/recover.go
package middleware

import (
    "fmt"
    "net/http"

    "github.com/maksimegorovdev/delivery-backend/platform/apperr"
    "github.com/maksimegorovdev/delivery-backend/platform/http/response"
)

// Recover — паника в хендлере не должна валить процесс и не должна отдавать
// голый 500 без конверта: отвечаем через response.Fail, как любая другая ошибка.
// Сам не логирует (Fail тоже не логирует, см. §5) — паника долетает до лога как
// обычная ошибка через middleware.Logger, который должен оборачивать Recover
// снаружи (см. порядок ниже).
func Recover(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if rec := recover(); rec != nil {
                response.Fail(w, r, apperr.Internal().Wrap(fmt.Errorf("panic: %v", rec)))
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```

```go
// platform/http/middleware/logger.go
package middleware

import (
    "log/slog"
    "net/http"
    "time"

    chimw "github.com/go-chi/chi/v5/middleware"

    "github.com/maksimegorovdev/delivery-backend/platform/apperr"
    "github.com/maksimegorovdev/delivery-backend/platform/appctx"
    "github.com/maksimegorovdev/delivery-backend/platform/logger"
)

// Logger — единственное место логирования HTTP-слоя: и обычный access-лог, и
// ошибка. chimw.WrapResponseWriter читает итоговый статус-код (сам chimw.Logger
// не используем — у него не slog, а свой pretty-printer, не вписывается в
// logger.New()). appctx.WithErrBox заводит пустой ErrBox и кладёт его в контекст
// до next.ServeHTTP — response.Fail (вызванный где угодно глубже: из хендлера или
// из Recover) заполняет этот box через appctx.SetError, а Logger читает его после
// next.ServeHTTP и логирует уже готовую *apperr.Error нужным уровнем
// (e.Code.Level()) — тот же принцип, что у platform/grpc/interceptors.Logger,
// только там источник ошибки — возврат handler'а, а тут — appctx.ErrBox, потому
// что http.Handler ничего не возвращает.
func Logger(log *slog.Logger) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
            ctx, box := appctx.WithErrBox(r.Context())

            next.ServeHTTP(ww, r.WithContext(ctx))

            if box.Err != nil {
                e := apperr.From(box.Err)
                log.LogAttrs(r.Context(), e.Code.Level(), "http request failed",
                    slog.String("method", r.Method),
                    slog.String("path", r.URL.Path),
                    slog.Duration("duration", time.Since(start)),
                    slog.String("request_id", appctx.GetRequestID(r.Context())),
                    logger.Err(e), // e.LogValue(): code + reason + cause + violations
                )
                return
            }

            log.LogAttrs(r.Context(), slog.LevelInfo, "http request handled",
                slog.String("method", r.Method),
                slog.String("path", r.URL.Path),
                slog.Int("status", ww.Status()),
                slog.Duration("duration", time.Since(start)),
                slog.String("request_id", appctx.GetRequestID(r.Context())),
            )
        })
    }
}
```

```go
// platform/http/middleware/fake_auth.go
package middleware

import (
    "net/http"

    "github.com/maksimegorovdev/delivery-backend/platform/appctx"
)

// FakeAuth — временная замена реального auth (см. architecture.md §8). Когда
// появится настоящий auth, меняется только этот middleware — appctx.GetUserID
// в хендлерах не трогается.
func FakeAuth(defaultUserID string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            uid := r.Header.Get("X-User-Id")
            if uid == "" {
                uid = defaultUserID
            }
            next.ServeHTTP(w, r.WithContext(appctx.WithUserID(r.Context(), uid)))
        })
    }
}
```

Порядок в chi (`r.Use(...)`, снаружи → внутрь): `RequestID` → `Logger` → `Recover` →
`FakeAuth`. `RequestID` должен быть первым — иначе `Logger`/`Recover` ничего не найдут
в `appctx.GetRequestID`. `Logger` должен быть снаружи `Recover` (а не наоборот, как
может показаться интуитивным): `Logger` заводит `ErrBox` и вызывает `next.ServeHTTP`,
`Recover` — следующий по цепочке — ловит панику через свой `defer`, отдаёт её через
`response.Fail` (тот кладёт её в тот же `ErrBox`) и возвращает управление нормально;
`Recover` должен полностью погасить панику, прежде чем стек развернётся обратно в
`Logger` — тогда код после `next.ServeHTTP` в `Logger` гарантированно выполнится и
увидит заполненный `box.Err`, что для обычной ошибки, что для пойманной паники. Если
бы `Recover` был снаружи `Logger` (как в первой версии этого документа), паника
развернула бы стек мимо кода `Logger` после `next.ServeHTTP`, и запрос вообще не
попал бы в лог.

---

## 8. `domain/order.go` — общий тип `Order`/`OrderItem`

`Order`/`OrderItem` — не вывод одного usecase, а тип, которым будут пользоваться
несколько usecase'ов gateway (`CreateOrder` сейчас, `GetOrder`/`ListOrders` позже,
§18 п.12) и, потенциально, другие ресурсы при проксировании (`user`, `product`).
Если бы это остался тип внутри `usecase` (как в первой версии этого документа), то
`GetOrder`/`ListOrders` либо дублировали бы его у себя, либо продолжали читать его из
пакета `order.go`, где ему формально не место — он не про оркестрацию конкретного
вызова, а про форму заказа как таковую. Отдельный пакет `domain` убирает эту
двусмысленность: один канонический тип на весь gateway.

Пакет тонкий — без инвариантов и без фабрики вида `domain.NewOrder(...)`: gateway
не создаёт заказ, только отображает то, что вернул `order`-service, поэтому защищать
нечего. Тот же приём, каким `order`-сервис держит `domain.User`/`domain.Address` —
зеркало чужого proto без своих правил (см. [order-service.md](order-service.md) §2).

```go
package domain

import "time"

type OrderItem struct {
    ID          string
    ProductID   string
    ProductName string
    Quantity    int32
    UnitPrice   int64 // копейки
}

type Order struct {
    ID              string
    UserID          string
    Status          string
    Items           []OrderItem
    TotalAmount     int64 // копейки
    DeliveryAddress string
    CreatedAt       time.Time
    UpdatedAt       time.Time
}
```

---

## 9. `usecase/order.go` — тонкая оркестрация

```go
package usecase

import (
    "context"

    "github.com/maksimegorovdev/delivery-backend/services/gateway/internal/domain"
)

type CreateOrderItemInput struct {
    ProductID string
    Quantity  int32
}

type CreateOrderInput struct {
    UserID    string
    AddressID string
    Items     []CreateOrderItemInput
}

// Порт: gateway не знает, что это gRPC — только «создай заказ».
type OrderProvider interface {
    CreateOrder(ctx context.Context, in CreateOrderInput) (domain.Order, error)
}

type OrderUsecaseDeps struct {
    Orders OrderProvider
}

type OrderUsecase struct {
    orders OrderProvider
}

func NewOrderUsecase(deps OrderUsecaseDeps) *OrderUsecase {
    return &OrderUsecase{orders: deps.Orders}
}

// Сейчас чистый проход через порт — так и задумано (architecture.md §6.8:
// «usecase = оркестрация вызовов»). Слой не лишний: он держит транспорт (chi)
// и адаптер (gRPC-клиент) разделёнными уже сейчас, и это единственное место,
// где появится вторая проверка перед CreateOrder, если она понадобится —
// без него пришлось бы протаскивать client прямо в http-хендлер.
func (uc *OrderUsecase) CreateOrder(ctx context.Context, in CreateOrderInput) (domain.Order, error) {
    return uc.orders.CreateOrder(ctx, in)
}
```

---

## 10. `client/order.go` — gRPC-клиент к `order`-service

```go
package client

import (
    "context"

    "google.golang.org/grpc"

    "github.com/maksimegorovdev/delivery-backend/platform/apperr/grpcerr"
    orderv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/order/v1"
    "github.com/maksimegorovdev/delivery-backend/services/gateway/internal/domain"
    "github.com/maksimegorovdev/delivery-backend/services/gateway/internal/usecase"
)

type OrderClient struct {
    orders orderv1.OrderServiceClient
}

func NewOrderClient(conn *grpc.ClientConn) *OrderClient {
    return &OrderClient{orders: orderv1.NewOrderServiceClient(conn)}
}

func (c *OrderClient) CreateOrder(ctx context.Context, in usecase.CreateOrderInput) (domain.Order, error) {
    items := make([]*orderv1.CreateOrderItem, 0, len(in.Items))
    for _, it := range in.Items {
        items = append(items, &orderv1.CreateOrderItem{
            ProductId: it.ProductID,
            Quantity:  it.Quantity,
        })
    }

    resp, err := c.orders.CreateOrder(ctx, &orderv1.CreateOrderRequest{
        UserId:    in.UserID,
        AddressId: in.AddressID,
        Items:     items,
    })
    if err != nil {
        return domain.Order{}, grpcerr.Map(err) // status -> *apperr.Error
    }
    return protoToOrder(resp.GetOrder()), nil
}

func protoToOrder(o *orderv1.Order) domain.Order {
    items := make([]domain.OrderItem, 0, len(o.GetItems()))
    for _, it := range o.GetItems() {
        items = append(items, domain.OrderItem{
            ID:          it.GetId(),
            ProductID:   it.GetProductId(),
            ProductName: it.GetProductName(),
            Quantity:    it.GetQuantity(),
            UnitPrice:   it.GetUnitPrice(),
        })
    }
    return domain.Order{
        ID:              o.GetId(),
        UserID:          o.GetUserId(),
        Status:          statusFromProto(o.GetStatus()),
        Items:           items,
        TotalAmount:     o.GetTotalAmount(),
        DeliveryAddress: o.GetDeliveryAddress(),
        CreatedAt:       o.GetCreatedAt().AsTime(),
        UpdatedAt:       o.GetUpdatedAt().AsTime(),
    }
}

// statusFromProto — обратное отображение к order-service statusToProto
// (см. order-service.md §6): наружу отдаём те же строчные значения,
// что order хранит внутри себя (domain.OrderStatus), а не ORDER_STATUS_*.
func statusFromProto(s orderv1.OrderStatus) string {
    switch s {
    case orderv1.OrderStatus_ORDER_STATUS_CREATED:
        return "created"
    case orderv1.OrderStatus_ORDER_STATUS_PAID:
        return "paid"
    case orderv1.OrderStatus_ORDER_STATUS_CONFIRMED:
        return "confirmed"
    case orderv1.OrderStatus_ORDER_STATUS_ASSEMBLING:
        return "assembling"
    case orderv1.OrderStatus_ORDER_STATUS_ASSEMBLED:
        return "assembled"
    case orderv1.OrderStatus_ORDER_STATUS_COURIER_ASSIGNED:
        return "courier_assigned"
    case orderv1.OrderStatus_ORDER_STATUS_DELIVERING:
        return "delivering"
    case orderv1.OrderStatus_ORDER_STATUS_DELIVERED:
        return "delivered"
    case orderv1.OrderStatus_ORDER_STATUS_CANCELED:
        return "canceled"
    default:
        return "unspecified"
    }
}
```

`grpcerr.Map` уже реализован (см. `platform/apperr/grpcerr/grpcerr.go`, уже используется в
`services/order/internal/client/{user,product}.go`) — он разворачивает
`ErrorInfo` обратно в точный `apperr.Code` (не теряет `NOT_FOUND` в общий `INTERNAL`).
Ошибку клиент не оборачивает и не логирует — это сделает `response.Fail` на границе
HTTP-хендлера (§11), один раз.

---

## 11. `transport/http/v1/order` — DTO, валидация, маппинг, хендлер

Пакет называется `order`, а не `v1`, потому что `v1` — это версия HTTP-контракта
целиком, а не одна сущность: сюда же позже лягут `v1/user`, `v1/product` (см. §1) со
своими `dto.go`/`handler.go`. Если бы файлы order лежали прямо в `v1/`, при добавлении
второй сущности их пришлось бы переносить в свой подпакет и переписывать импорты —
поэтому подпакет заводится сразу, даже пока в `v1/` только одна сущность.

### 11.1 `dto.go`

```go
package order

import (
    "time"

    validation "github.com/go-ozzo/ozzo-validation/v4"
    "github.com/go-ozzo/ozzo-validation/v4/is"
)

type CreateOrderItemDTO struct {
    ProductID string `json:"product_id"`
    Quantity  int32  `json:"quantity"`
}

func (d CreateOrderItemDTO) Validate() error {
    return validation.ValidateStruct(&d,
        validation.Field(&d.ProductID, validation.Required, is.UUID),
        validation.Field(&d.Quantity, validation.Required, validation.Min(int32(1))),
    )
}

type CreateOrderRequestDTO struct {
    AddressID string                `json:"address_id"`
    Items     []CreateOrderItemDTO  `json:"items"`
}

func (d CreateOrderRequestDTO) Validate() error {
    return validation.ValidateStruct(&d,
        validation.Field(&d.AddressID, validation.Required, is.UUID),
        validation.Field(&d.Items, validation.Required, validation.Length(1, 0)),
        // элементы Items валидируются автоматически: ozzo-validation вызывает
        // Validate() у каждого элемента срeза, если тип реализует Validatable —
        // отдельный Each(...) не нужен.
    )
}

type OrderItemResponseDTO struct {
    ID          string `json:"id"`
    ProductID   string `json:"product_id"`
    ProductName string `json:"product_name"`
    Quantity    int32  `json:"quantity"`
    UnitPrice   int64  `json:"unit_price"`
}

type OrderResponseDTO struct {
    ID              string                  `json:"id"`
    UserID          string                  `json:"user_id"`
    Status          string                  `json:"status"`
    Items           []OrderItemResponseDTO  `json:"items"`
    TotalAmount     int64                   `json:"total_amount"`
    DeliveryAddress string                  `json:"delivery_address"`
    CreatedAt       time.Time               `json:"created_at"`
    UpdatedAt       time.Time               `json:"updated_at"`
}
```

### 11.2 Валидация — `platform/apperr/ozzoerr`, не локальный `validation.go`

`flatten`/`toValidationErr` — не про заказы, а про ozzo-validation вообще, поэтому
живут не в `v1/order`, а в общем `platform/apperr/ozzoerr` (§4.3): иначе `v1/user`/
`v1/product` (§1, §17 п.14) заводили бы себе точно такой же файл под другим именем
пакета. `v1/order` (и любой другой `v1/<entity>`) сам `ozzoerr.Map` не вызывает —
это делает `platform/http/bind` (§4.4) внутри `bind.JSON[T]`, а `handler.go` (§11.4)
просто вызывает `bind.JSON[CreateOrderRequestDTO](r)`. Свой `validation.go` в этом
пакете не нужен.

### 11.3 `mapper.go`

```go
package order

import (
    "github.com/maksimegorovdev/delivery-backend/services/gateway/internal/domain"
    "github.com/maksimegorovdev/delivery-backend/services/gateway/internal/usecase"
)

func toCreateOrderInput(userID string, dto CreateOrderRequestDTO) usecase.CreateOrderInput {
    items := make([]usecase.CreateOrderItemInput, 0, len(dto.Items))
    for _, it := range dto.Items {
        items = append(items, usecase.CreateOrderItemInput{
            ProductID: it.ProductID,
            Quantity:  it.Quantity,
        })
    }
    return usecase.CreateOrderInput{UserID: userID, AddressID: dto.AddressID, Items: items}
}

func toOrderResponseDTO(o domain.Order) OrderResponseDTO {
    items := make([]OrderItemResponseDTO, 0, len(o.Items))
    for _, it := range o.Items {
        items = append(items, OrderItemResponseDTO{
            ID:          it.ID,
            ProductID:   it.ProductID,
            ProductName: it.ProductName,
            Quantity:    it.Quantity,
            UnitPrice:   it.UnitPrice,
        })
    }
    return OrderResponseDTO{
        ID:              o.ID,
        UserID:          o.UserID,
        Status:          o.Status,
        Items:           items,
        TotalAmount:     o.TotalAmount,
        DeliveryAddress: o.DeliveryAddress,
        CreatedAt:       o.CreatedAt,
        UpdatedAt:       o.UpdatedAt,
    }
}
```

### 11.4 `handler.go` (+ Swagger-аннотации, см. §12)

```go
package order

import (
    "context"
    "net/http"

    "github.com/maksimegorovdev/delivery-backend/platform/appctx"
    "github.com/maksimegorovdev/delivery-backend/platform/http/bind"
    "github.com/maksimegorovdev/delivery-backend/platform/http/response"
    "github.com/maksimegorovdev/delivery-backend/services/gateway/internal/domain"
    "github.com/maksimegorovdev/delivery-backend/services/gateway/internal/usecase"
)

// интерфейс потребителя объявлен здесь (как в order-service: OrderUsecase рядом
// с transport, не рядом с usecase) — handler зависит от узкого контракта.
type OrderUsecase interface {
    CreateOrder(ctx context.Context, in usecase.CreateOrderInput) (domain.Order, error)
}

// Без *slog.Logger: хендлер ничего не логирует (см. §5, §7) — response.Fail
// только формирует ответ, а логирует его middleware.Logger на границе запроса.
type OrderHandler struct {
    uc OrderUsecase
}

func NewOrderHandler(uc OrderUsecase) *OrderHandler {
    return &OrderHandler{uc: uc}
}

// CreateOrder godoc
//
//  @Summary      Создать заказ
//  @Description  Создаёт заказ из позиций каталога и адреса доставки; user_id берётся из fake-auth контекста
//  @Tags         orders
//  @Accept       json
//  @Produce      json
//  @Param        request body CreateOrderRequestDTO true "Данные заказа"
//  @Success      201 {object} response.Envelope{data=OrderResponseDTO}
//  @Failure      400 {object} response.Envelope{error=response.ErrorBody} "невалидный запрос"
//  @Failure      404 {object} response.Envelope{error=response.ErrorBody} "адрес/товар не найден"
//  @Failure      503 {object} response.Envelope{error=response.ErrorBody} "order-service недоступен"
//  @Router       /v1/orders [post]
func (h *OrderHandler) CreateOrder(w http.ResponseWriter, r *http.Request) {
    dto, err := bind.JSON[CreateOrderRequestDTO](r)
    if err != nil {
        response.Fail(w, r, err) // decode или ozzoerr.Map — обе уже *apperr.Error (bind.JSON, §4.4)
        return
    }

    in := toCreateOrderInput(appctx.GetUserID(r.Context()), dto)

    order, err := h.uc.CreateOrder(r.Context(), in)
    if err != nil {
        response.Fail(w, r, err) // *apperr.Error уже пришёл из client (grpcerr.Map)
        return
    }
    response.Created(w, r, toOrderResponseDTO(order))
}
```

`dto`, возвращённый `bind.JSON`, — переменная во внешней области видимости
`err` (`:=` на первой строке), поэтому второй `err` в блоке `h.uc.CreateOrder`
переиспользует её же (`order, err := ...`), как и раньше.

### 11.5 `router.go`

```go
// services/gateway/internal/transport/http/v1/order/router.go
package order

import "github.com/go-chi/chi/v5"

func Routes(r chi.Router, h *OrderHandler) {
    r.Post("/orders", h.CreateOrder)
}
```

```go
// services/gateway/internal/transport/http/router.go
package http

import (
    "github.com/go-chi/chi/v5"

    "github.com/maksimegorovdev/delivery-backend/services/gateway/internal/transport/http/v1/order"
)

type RouterDeps struct {
    Router       chi.Router
    OrderHandler *order.OrderHandler
}

func NewRouter(deps RouterDeps) {
    deps.Router.Route("/v1", func(r chi.Router) {
        order.Routes(r, deps.OrderHandler)
        // user.Routes(r, deps.UserHandler), product.Routes(...) — тем же способом позже
    })
}
```

Раскладка на будущее: `GET /v1/orders/{id}` — новый метод на `OrderHandler` + строка в
`order.Routes`, тот же `OrderUsecase`/новый порт. Прокси к `user`/`product` — новые
подпакеты `v1/user`, `v1/product` рядом с `v1/order`, каждый со своим
`dto.go`/`handler.go`/`router.go` и своей `Routes`, подключаемой в `NewRouter` тем же
способом. Версии контракта (`/v2/...`) — по образцу [architecture.md](architecture.md)
§6.7: новый пакет `v2/order/{handler.go, dto.go, mapper.go, router.go}` рядом с `v1/order`,
тот же usecase.

---

## 12. Swagger

Библиотеки: `github.com/swaggo/swag` (CLI, генерирует спецификацию из комментариев
над хендлерами) + `github.com/swaggo/http-swagger/v2` (обслуживает Swagger UI поверх
любого `http.Handler`, включая chi).

**Куда генерировать.** `swag init` кладёт `docs.go` + `swagger.json`/`swagger.yaml` в
Go-пакет внутри сервиса — `services/gateway/internal/docs`. Это **не** тот же каталог,
что и корневой `docs/` (markdown-документация проекта, где лежит и этот файл) — разные
уровни, разные форматы, коллизии нет, но при выборе `-o` не перепутать.

Верхнеуровневые аннотации — в `cmd/main.go`:

```go
// @title        Delivery Gateway API
// @version      1.0
// @description   HTTP-шлюз к order-service. Формат ответа: {meta, data, error}.
// @BasePath      /
package main
```

Аннотации на хендлере — см. §11.4 (`@Summary`, `@Param`, `@Success`, `@Failure`,
`@Router`). Форма `response.Envelope{data=OrderResponseDTO}` — генерик-переопределение
swaggo (доступно с версии, поддерживающей `{field=Type}`): в сваггере `data`
специализируется конкретным DTO вместо `any`, иначе swaggo сериализует `any` как
пустой `object` без полей.

Монтирование в роутере (`app.go`, после `NewRouter`):

```go
import (
    httpSwagger "github.com/swaggo/http-swagger/v2"

    _ "github.com/maksimegorovdev/delivery-backend/services/gateway/internal/docs" // side-effect: регистрирует SwaggerInfo
)

httpServer.Router().Get("/swagger/*", httpSwagger.WrapHandler)
```

Makefile-таргет (по образцу уже существующих в `services/gateway/Makefile`):

```makefile
.PHONY: swagger
swagger:
	swag init -g cmd/main.go -o internal/docs --parseInternal
```

`--parseInternal` нужен, потому что DTO (`CreateOrderRequestDTO` и т.п.) лежат в
`internal/...` — без флага swag их не увидит.

---

## 13. `app/app.go` — проводка

```go
package app

func New(ctx context.Context) (*App, error) {
    cfg, err := config.New()
    if err != nil {
        return nil, err
    }

    log := logger.New(
        logger.WithLevel(cfg.Log.Level),
        logger.WithFormat(cfg.Log.Format),
    ).With(slog.String("app_name", cfg.App.Name))
    slog.SetDefault(log)

    // gRPC client → order-service
    orderConn, err := grpcclient.New(cfg.OrderService.Addr)
    if err != nil {
        return nil, err
    }

    orderClient := client.NewOrderClient(orderConn)
    orderUsecase := usecase.NewOrderUsecase(usecase.OrderUsecaseDeps{Orders: orderClient})
    orderHandler := order.NewOrderHandler(orderUsecase)

    httpServer := httpserver.New(cfg.HTTP.Addr)
    httpServer.Router().Use(
        middleware.RequestID,
        middleware.Logger(log),
        middleware.Recover,
        middleware.FakeAuth(cfg.FakeAuth.DefaultUserID),
    )
    router.NewRouter(router.RouterDeps{Router: httpServer.Router(), OrderHandler: orderHandler})
    httpServer.Router().Get("/swagger/*", httpSwagger.WrapHandler)

    return &App{
        cfg:        cfg,
        log:        log,
        httpServer: httpServer,
        orderConn:  orderConn, // закрыть в Close()
    }, nil
}

func (a *App) Close() error {
    return a.orderConn.Close()
}
```

`Run(ctx)` не меняется — тот же `httpServer.Run(ctx)` внутри `errgroup`, что и сейчас
в `services/gateway/internal/app/app.go`.

---

## 14. go.mod — что добавится в `services/gateway`

```
github.com/go-chi/chi/v5                 // роутер (уже транзитивно через platform, но
                                          // handler/router-файлы импортируют его напрямую)
github.com/go-ozzo/ozzo-validation/v4
github.com/go-ozzo/ozzo-validation/v4/is
github.com/google/uuid
github.com/swaggo/swag
github.com/swaggo/http-swagger/v2
github.com/maksimegorovdev/delivery-backend/proto  (+ replace ../../proto, как в order/go.mod)
```

`google.golang.org/grpc` уже тянется через `platform` (тот же паттерн, что в
`services/order/go.mod`).

---

## 15. Ошибки — сквозной путь (сводка)

Полный разбор — [errors.md](errors.md) §10. Для gateway меняется только последний шаг:

```
order.CreateOrder (usecase/domain/repo)  → *apperr.Error{Code, Message, Err}
  → interceptors.Error() (order-service) → gRPC status + ErrorInfo{Reason: string(Code)}
  → client.OrderClient.CreateOrder        → grpcerr.Map(err) → *apperr.Error (точный Code восстановлен)
  → usecase.OrderUsecase.CreateOrder      → пробрасывает как есть
  → order.OrderHandler.CreateOrder        → response.Fail(w, r, err)
       → HTTP-статус = apperr.Code.HTTP()
       → JSON {"meta": {...}, "data": null, "error": {"code": ..., "message": ...}}
       → appctx.SetError кладёт err в ErrBox (§4.2)
  → middleware.Logger (после next.ServeHTTP) → лог WARN/ERROR — один раз, на границе
       gateway, и только тут (см. §5, §7)
```

Ошибки валидации тела (`json.Decode`, ozzo) возникают **в самом gateway**, до всякого
gRPC — тоже уходят через `response.Fail`, с кодом `INVALID_ARGUMENT` и, для составной
валидации, `Reason: VALIDATION_FAILED` + `Violations` (`platform/apperr/ozzoerr.Map`, §4.3).

---

## 16. Ручная проверка

1. `POST /v1/orders` с валидным `address_id` + существующими `product_id` → `201`,
   `data.status == "created"`, `data.total_amount` совпадает с суммой `unit_price * quantity`.
2. `product_id`, которого нет в `product`-сервисе → `404`, `error.code == "NOT_FOUND"`.
3. `address_id` не принадлежит `user_id` из fake-auth (или не существует) → `404`/`400`
   в зависимости от того, что вернёт `user`-service для `GetAddress`.
4. `items: []` (пустой список) → `400`, `error.reason == "VALIDATION_FAILED"`,
   `violations` содержит `{"field": "items", ...}`.
5. `quantity: 0` у одной из позиций → `400`, `violations` содержит
   `{"field": "items.0.quantity", ...}`.
6. Остановить `order`-service → `503`, `error.code == "UNAVAILABLE"` (см.
   [architecture.md](architecture.md) §3, сценарий 5, только теперь ответ идёт через
   gateway, а не напрямую от order).
7. `X-Request-Id` в заголовке запроса → тот же самый `X-Request-Id` в заголовке
   ответа и в `meta.request_id` тела.

---

## 17. Порядок реализации

1. `platform/appctx`: `user_id.go` (§4.1), `error_box.go` (§4.2).
2. `platform/apperr/ozzoerr`: `Flatten`/`Map` (§4.3).
3. `platform/http/bind`: `JSON[T]` — decode + `ozzoerr.Map(T.Validate())` (§4.4).
4. `platform/http/response`: `Envelope`, `Meta`, `ErrorBody`, `OK`/`Created`/`Fail` (§5) —
   `Fail` не логирует.
5. `platform/http/middleware`: `RequestID`, `Logger`, `Recover`, `FakeAuth` (§7), именно
   в этом порядке — `Logger` снаружи `Recover`.
6. `config`: `OrderService.Addr`, `FakeAuth.DefaultUserID`, `.env`/`.env.example` (§6).
7. `domain/order.go`: `Order`/`OrderItem` (§8).
8. `usecase/order.go`: `CreateOrderInput/Item`, порт `OrderProvider`, `OrderUsecaseDeps`,
   `OrderUsecase` (§9) — компилируется на моке порта, без реального клиента.
9. `client/order.go`: `OrderClient` над `orderv1.OrderServiceClient` (§10).
10. `transport/http/v1/order`: `dto.go`, `mapper.go`, `handler.go`, `router.go` (§11) —
    decode+валидация через `bind.JSON` (§4.4), свой `validation.go` не заводим.
11. `transport/http/router.go`: монтирование `/v1` (§11.5).
12. `app/app.go`: `grpcclient.New` к order-сервису → проводка → middlewares → router (§13).
13. `swag init` → `internal/docs`, подключить `httpSwagger.WrapHandler` на `/swagger/*` (§12).
14. Ручная проверка по чек-листу §16 (нужен живой `order` + `user` + `product`, как
    описано в `../docker-compose.yml`).
15. (Задел, не сейчас) `GET /v1/orders/{id}` — новый handler + порт `GetOrder` в
    `OrderProvider`/`OrderUsecase`, тот же паттерн.

---

## 18. Итоговое дерево

```
platform/
  appctx/
    user_id.go                     # + WithUserID/GetUserID
    error_box.go                    # + ErrBox, WithErrBox, SetError
  apperr/
    ozzoerr/
      ozzoerr.go                    # Flatten/Map: ozzo validation.Errors → []apperr.Violation
  http/
    bind/
      bind.go                       # JSON[T]: decode + ozzoerr.Map(T.Validate())
    response/
      response.go                  # Envelope, Meta, ErrorBody, OK, Created, Fail (не логирует)
    middleware/
      request_id.go  logger.go  recover.go  fake_auth.go   # Logger — единственный логгер HTTP-слоя

services/gateway/internal/
  app/app.go                        # +orderConn +orderClient +orderUsecase +orderHandler
                                    #   +middlewares (RequestID→Logger→Recover→FakeAuth)
                                    #   +swagger route (закрыть orderConn в Close)
  config/config.go                  # +OrderService.Addr +FakeAuth.DefaultUserID
  domain/
    order.go                        # Order/OrderItem — общий тип для usecase/order и client/order
  usecase/
    order.go                        # CreateOrderInput/Item, OrderProvider, OrderUsecaseDeps,
                                    #   OrderUsecase (возвращает domain.Order)
  client/
    order.go                        # OrderClient (orderv1 → domain.Order) + statusFromProto
  transport/http/
    router.go                       # RouterDeps + NewRouter
    v1/
      order/
        dto.go  mapper.go  handler.go  router.go   # decode+валидация — через platform/http/bind
      # user/, product/ — тем же паттерном позже
  docs/                              # сгенерировано swag init
```
