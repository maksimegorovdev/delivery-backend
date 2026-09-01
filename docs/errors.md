# Delivery Platform — Ошибки: коды и маппинг

Справочник по пакету `platform/apperr`: значение каждого доменного кода, его отображение
в gRPC-статус, HTTP-статус и уровень лога, а также обратный маппинг из gRPC и из Postgres.

Принцип: через все слои идёт одна доменная ошибка `*apperr.Error`. У неё два лица —
`Message` (безопасный текст для клиента) и `Err` (внутренняя цепочка причин, только в логи).
Весь маппинг между кодами живёт рядом с типом `Code`, транспортные пакеты друг о друге не знают.

**Конвенция именования.** Имена доменных кодов совпадают с каноническими именами gRPC-кодов
(`google.golang.org/grpc/codes`). Отсюда два следствия:

- `Code ↔ gRPC` — **полная биекция**: у каждого из 16 нестатусных кодов `codes.*` есть
  тёзка-константа `apperr.Code`, и наоборот. Обратный маппинг в gateway ничего не
  «схлопывает» и не гадает — `InvalidArgument` всегда даёт `INVALID_ARGUMENT`, и наоборот.
  `OK` не в счёт — это не ошибка (`nil`).
- `Code → HTTP` — фиксированная таблица из `google.rpc.Code` (тот же маппинг, что у grpc-gateway).

HTTP-именами (`BAD_REQUEST`, `CONFLICT`, `UNAUTHORIZED`, …) словарь не пользуется: набор
HTTP-статусов беднее (один `400` на всё, один `409` на два разных случая), а обратного
маппинга «из HTTP» в системе нет — HTTP это крайний edge к внешнему клиенту.

Более тонкие различия внутри одного кода (не JSON vs плохое значение; один невалидный
аргумент vs форма целиком; нет токена vs неверный пароль) несёт **опциональная под-причина**
`Error.Reason` — она не влияет на транспортный маппинг, только помогает клиенту ветвиться
(см. §1, «Под-причины»).

---

## 1. Доменные коды `apperr.Code`

| Код | Значение | Когда возвращать | Типичный источник |
|---|---|---|---|
| `INTERNAL` | Непредвиденная ошибка сервера. Клиент ничего не может с этим сделать. Наружу — только generic-текст, детали в лог. | Любая незапланированная ошибка, паника, «этого не должно было случиться», неизвестная ошибка от зависимости. | Баг в коде, сбой сериализации, неизвестный SQLSTATE, `nil`-разыменование. |
| `INVALID_ARGUMENT` | Запрос не прошёл проверку: тело не разобралось (не JSON, не тот тип поля, не распарсился protobuf), значение вне допустимого диапазона, ссылка на несуществующую сущность в поле, нарушение инварианта домена. Составная валидация нескольких полей несёт список `Violations` и под-причину `VALIDATION_FAILED`; неразобранное тело — под-причину `REQUEST_BODY_MALFORMED`. | Декодирование запроса в HTTP-хендлере, валидация DTO/формы, валидация в service-слое, `FOREIGN KEY` / `CHECK` / `NOT NULL` от Postgres. |
| `NOT_FOUND` | Запрошенная сущность не существует или недоступна текущему пользователю (чтобы не раскрывать существование). | `GetByID` не нашёл строку, переход по несуществующему идентификатору. | `pgx.ErrNoRows` в репозитории. |
| `ALREADY_EXISTS` | Сущность с такими уникальными атрибутами уже есть. Повторное создание. | Нарушение `UNIQUE`-индекса при вставке, повторная регистрация. | `UNIQUE_VIOLATION` (SQLSTATE `23505`). |
| `ABORTED` | Операция прервана из-за конфликта конкурентного доступа или состояния ресурса. Клиенту стоит повторить всю последовательность read-modify-write после изменения состояния. В отличие от `ALREADY_EXISTS` — это не про уникальность, а про порядок операций / гонку. | Оптимистическая блокировка (версия изменилась), заказ уже оплачен/отменён, race при переходе статуса. | `SERIALIZATION_FAILURE` / `DEADLOCK_DETECTED` (если решено считать их конфликтом, а не `UNAVAILABLE`), проверка версии в service-слое. |
| `UNAUTHENTICATED` | Запрос без валидных учётных данных: токен отсутствует, истёк, подпись неверна — либо учётные данные предъявлены, но не совпали (неверный логин/пароль, под-причина `INVALID_CREDENTIALS`). Клиент не идентифицирован. Наружу — generic-текст без указания, что именно не так; для `INVALID_CREDENTIALS` — фиксированный `invalid credentials` (тоже не раскрывает, логин или пароль). `Message` разработчика в этот код не подставляется. | Middleware аутентификации, невалидный / отсутствующий `Authorization`, хендлер логина. | Auth-middleware gateway, service-слой аутентификации. |
| `PERMISSION_DENIED` | Клиент идентифицирован, но не имеет прав на операцию/ресурс. | Проверка ролей/владения ресурсом: пользователь пытается прочитать чужой заказ. | Проверка доступа в service-слое. |
| `DEADLINE_EXCEEDED` | Операция не уложилась в дедлайн: истёк таймаут `context.Context`, долгий запрос к БД прерван по времени. Явную отмену вызова / отвал клиента см. `CANCELED`. | Дедлайн вызова превышен, `context.DeadlineExceeded`. | Истёкший по таймауту `context`, `QUERY_CANCELED` (SQLSTATE `57014`), таймаут вызова зависимости. |
| `UNAVAILABLE` | Зависимость временно недоступна: нет соединения с БД/Kafka/соседним сервисом, пул исчерпан. Операцию имеет смысл повторить с backoff. | Не удалось подключиться к Postgres, gRPC-вызов вернул `Unavailable`, пул соединений пуст. | Ошибка dial/connect, `pgxpool` не выдал соединение, `codes.Unavailable` от апстрима. |
| `UNKNOWN` | Непрозрачный сбой без доменной классификации: gRPC-статус без кода, код из чужого адресного пространства, ошибка стороннего сервиса/прокси/mesh. Наружу — generic-текст, как у `INTERNAL`. | Только на границе gateway при разборе ответа не-нашего апстрима (нет `ErrorInfo` с точным кодом). Внутри процесса не порождается. | `CodeFromGRPC(codes.Unknown)`, сторонний gRPC. |
| `CANCELED` | Вызов не завершён, потому что закончился `context` вызывающей стороны: клиент закрыл соединение или явно отменил запрос. Ответ, как правило, отдавать уже некому. | Даунстрим вернул `Canceled` (наш `r.Context()` отменился), `errors.Is(err, context.Canceled)`. | Отвал клиента, отменённый родительский контекст. |
| `RESOURCE_EXHAUSTED` | Исчерпан лимит или квота: rate limit, слишком большой запрос/страница, нехватка места. Клиенту стоит сбавить темп и повторить с backoff (по возможности учитывая `Retry-After`). | Rate-limiter, проверка размера тела / `page_size`, пользовательская квота. | Middleware лимитера в gateway, service-слой. |
| `FAILED_PRECONDITION` | Система не в том состоянии для операции, и повтор **без изменения состояния** не поможет (в отличие от `ABORTED` — там это гонка). Пример: удаление непустого ресурса, действие над сущностью в неподходящем статусе. | Проверка бизнес-предусловий в service-слое. | Service-слой, инварианты состояния. |
| `OUT_OF_RANGE` | Аргумент синтаксически валиден, но вне допустимого диапазона для текущего состояния: страница за последней, смещение за концом, интервал вне границ. В отличие от `INVALID_ARGUMENT`, может стать валидным при изменении состояния. | Пагинация, диапазонные и курсорные запросы. | Service-слой. |
| `UNIMPLEMENTED` | Метод не реализован или отключён на этом сервере / в этой версии. | Заглушка хендлера; вызов метода, которого нет в развёрнутой версии сервиса. | Stub-хендлер, рассинхрон версий client/server. |
| `DATA_LOSS` | Невосстановимая потеря или повреждение данных. Наружу — generic-текст. | Payload, целостность которого нельзя восстановить; повреждение в хранилище. | Слой хранилища, десериализация критичных данных. |

### Свойства кодов

- **Retryable (клиенту можно повторить):** `DEADLINE_EXCEEDED`, `UNAVAILABLE`,
  `RESOURCE_EXHAUSTED` — с backoff; `ABORTED` — после изменения состояния;
  `UNAUTHENTICATED` — с валидными учётными данными.
- **Не retryable:** `INVALID_ARGUMENT`, `NOT_FOUND`, `ALREADY_EXISTS`,
  `PERMISSION_DENIED`, `FAILED_PRECONDITION`, `OUT_OF_RANGE`, `UNIMPLEMENTED`,
  `CANCELED` — повтор того же запроса даст тот же результат (либо отвечать уже некому).
- **`INTERNAL` / `UNKNOWN` / `DATA_LOSS`** — retry на усмотрение клиента, обычно с
  backoff и ограничением попыток.
- **Наружу без деталей:** `INTERNAL`, `UNKNOWN`, `DATA_LOSS`, `UNAVAILABLE`,
  `DEADLINE_EXCEEDED`, `UNAUTHENTICATED` — `Public()` не отдаёт `Message` наружу. Для
  первых пяти причина — утечка внутренних подробностей; для `UNAUTHENTICATED` — политика
  «не раскрывать, что именно не так» (anti-enumeration). Текст берётся из `Code.Public()`
  (generic) либо из таблицы `publicByReason` по под-причине (`INVALID_CREDENTIALS` →
  `invalid credentials`) — набор закрыт, разработчик свой текст в эти коды не подставляет.
  У них и конструкторы без сообщения (`apperr.Internal()`, `apperr.Unauthenticated()`, …):
  диагностику несёт `.Wrap(err)` (в лог полем `cause`), произвольную ошибку в `INTERNAL`
  нормализует `apperr.From(err)`. Остальные коды отдают `Message`, заданный разработчиком
  (он обязан быть безопасным).

### Под-причины (`Error.Reason`)

Опциональная строка, уточняющая код для ветвления на клиенте. **Не влияет** на
`GRPC()` / `HTTP()` / `Level()` — транспортный маппинг идёт только по `Code`.

| Под-причина | Уточняет | Смысл |
|---|---|---|
| `REQUEST_BODY_MALFORMED` | `INVALID_ARGUMENT` | Тело запроса не удалось разобрать (не JSON, не тот тип, битый protobuf). Ошибка формата, не бизнес-логики. |
| `VALIDATION_FAILED` | `INVALID_ARGUMENT` | Составная ошибка валидации: несколько полей невалидны одновременно, несёт `Violations`. |
| `INVALID_CREDENTIALS` | `UNAUTHENTICATED` | Учётные данные предъявлены, но не совпали. Наружу — фиксированный `invalid credentials` (из `publicByReason`), не раскрывает, логин или пароль неверен. |

Транспорт: gRPC кладёт под-причину в `ErrorInfo.Metadata["reason"]`, HTTP — в поле
`reason` JSON-ответа. Новые под-причины добавляются здесь без изменения набора кодов.

---

## 2. gRPC codes (`google.golang.org/grpc/codes`)

Полный список кодов gRPC и их смысл. У каждого (кроме `OK`) есть тёзка-константа
`apperr.Code`; жирным — наиболее частые в этом сервисе.

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
| **`Aborted`** | 10 | Операция прервана из-за конфликта конкурентного доступа: неудачная транзакция, потеря оптимистической блокировки. Клиенту обычно стоит повторить всю последовательность (read-modify-write). |
| `OutOfRange` | 11 | Операция вышла за допустимый диапазон (например, seek за пределы файла). В отличие от `InvalidArgument`, этот код указывает на проблему, которая пройдёт при изменении состояния системы. |
| `Unimplemented` | 12 | Операция не реализована / не поддерживается на этом сервере. |
| **`Internal`** | 13 | Внутренняя ошибка. Сломаны инварианты, на которые рассчитывает система. Зарезервировано под серьёзные ошибки. |
| **`Unavailable`** | 14 | Сервис сейчас недоступен. Обычно временно — клиент может повторить с backoff. Не всякая неидемпотентная операция безопасна для повтора. |
| `DataLoss` | 15 | Невосстановимая потеря или повреждение данных. |
| **`Unauthenticated`** | 16 | Запрос не содержит валидных учётных данных для операции. |

Таблица маппинга полная — «резервных» gRPC-кодов не осталось. Часть доменных кодов
(`OUT_OF_RANGE`, `DATA_LOSS`, `UNKNOWN`) в этом сервисе почти не порождается вручную и
держится ради симметрии round-trip'а gateway ↔ сервис.

---

## 3. HTTP статусы

Значение статусов, которые отдаёт `Code.HTTP()`.

| Статус | Имя | Значение |
|---|---|---|
| `400` | Bad Request | Сервер не может обработать запрос из-за ошибки клиента: битый синтаксис, невалидное тело, неверные параметры. Повтор без изменений бессмыслен. |
| `401` | Unauthorized | Точнее — «Unauthenticated». Нет валидной аутентификации. Клиент может повторить с корректными учётными данными. Ответ по спецификации должен нести `WWW-Authenticate`. |
| `403` | Forbidden | Сервер понял запрос, но отказывает в доступе. Аутентификация не поможет — прав нет. Повтор бессмыслен. |
| `404` | Not Found | Ресурс не найден. Также используется, когда сервер не хочет раскрывать существование ресурса (вместо `403`). |
| `409` | Conflict | Запрос конфликтует с текущим состоянием ресурса: нарушение уникальности (`ALREADY_EXISTS`), конкурентное изменение / конфликт версий (`ABORTED`). Клиент может разрешить конфликт и повторить. |
| `429` | Too Many Requests | Клиент превысил rate limit или квоту (`RESOURCE_EXHAUSTED`). Повторить позже, в идеале — по `Retry-After`. |
| `499` | Client Closed Request | Нестандартный код (расширение nginx). Клиент закрыл соединение / отменил запрос до ответа (`CANCELED`); тело ответа обычно уже никто не читает. |
| `500` | Internal Server Error | Непредвиденная ошибка на сервере. Клиенту не сообщаются детали. |
| `501` | Not Implemented | Сервер не поддерживает функциональность, нужную для ответа (`UNIMPLEMENTED`). |
| `503` | Service Unavailable | Сервер временно не может обработать запрос: перегрузка, недоступная зависимость, обслуживание. Можно повторить позже (по возможности с `Retry-After`). |
| `504` | Gateway Timeout | Сервер, выступая шлюзом/прокси, не дождался ответа от апстрима в отведённый срок. В нашем случае — превышен дедлайн вызова зависимости (БД, соседний сервис). |

Почему `DEADLINE_EXCEEDED → 504`, а не `408 Request Timeout`: `408` означает, что *клиент*
слишком медленно слал запрос и сервер закрыл простаивающее соединение. У нас же дедлайн
истекает на стороне сервера при обращении к нижестоящей зависимости — это семантика шлюза,
`504`. Это же значение даёт официальный маппинг `google.rpc.Code.DEADLINE_EXCEEDED`.

---

## 4. Сводная таблица маппинга

`apperr.Code` → gRPC (`Code.GRPC()`), HTTP (`Code.HTTP()`), уровень лога (`Code.Level()`).

| `apperr.Code` | gRPC code | HTTP | slog level | Retryable |
|---|---|---|---|---|
| `CANCELED` | `Canceled` (1) | `499` | `WARN` | нет (отвечать некому) |
| `UNKNOWN` | `Unknown` (2) | `500` | `ERROR` | на усмотрение клиента |
| `INVALID_ARGUMENT` | `InvalidArgument` (3) | `400` | `WARN` | нет |
| `DEADLINE_EXCEEDED` | `DeadlineExceeded` (4) | `504` | `ERROR` | да, с backoff |
| `NOT_FOUND` | `NotFound` (5) | `404` | `WARN` | нет |
| `ALREADY_EXISTS` | `AlreadyExists` (6) | `409` | `WARN` | нет |
| `PERMISSION_DENIED` | `PermissionDenied` (7) | `403` | `WARN` | нет |
| `RESOURCE_EXHAUSTED` | `ResourceExhausted` (8) | `429` | `WARN` | да, с backoff |
| `FAILED_PRECONDITION` | `FailedPrecondition` (9) | `400` | `WARN` | нет |
| `ABORTED` | `Aborted` (10) | `409` | `WARN` | да, после изменения состояния |
| `OUT_OF_RANGE` | `OutOfRange` (11) | `400` | `WARN` | нет (пройдёт при смене состояния) |
| `UNIMPLEMENTED` | `Unimplemented` (12) | `501` | `ERROR` | нет |
| `INTERNAL` | `Internal` (13) | `500` | `ERROR` | на усмотрение клиента |
| `UNAVAILABLE` | `Unavailable` (14) | `503` | `ERROR` | да, с backoff |
| `DATA_LOSS` | `DataLoss` (15) | `500` | `ERROR` | нет |
| `UNAUTHENTICATED` | `Unauthenticated` (16) | `401` | `WARN` | да, с валидными данными |
| *(гипотетический будущий код)* | `Internal` (13) | `500` | `ERROR` | — |

Под-причина (`Reason`) в этой таблице не участвует — она едет отдельным полем и на статусы
не влияет.

---

## 5. Обратный маппинг: gRPC code → `apperr.Code`

Используется в gateway (`grpcerr.FromStatus`), когда HTTP-слой получает ответ от gRPC-сервиса.
Имена совпадают, поэтому маппинг **тотально биективен**: каждый из 16 нестатусных gRPC-кодов
переходит в свою доменную тёзку, и наоборот. `OK` — не ошибка (`nil`). Ничего не «схлопывается»;
ветка `default → INTERNAL` в коде оставлена только на случай будущих кодов gRPC.

| gRPC code | `apperr.Code` |
|---|---|
| `OK` (0) | *(не ошибка, `nil`)* |
| `Canceled` (1) | `CANCELED` |
| `Unknown` (2) | `UNKNOWN` |
| `InvalidArgument` (3) | `INVALID_ARGUMENT` |
| `DeadlineExceeded` (4) | `DEADLINE_EXCEEDED` |
| `NotFound` (5) | `NOT_FOUND` |
| `AlreadyExists` (6) | `ALREADY_EXISTS` |
| `PermissionDenied` (7) | `PERMISSION_DENIED` |
| `ResourceExhausted` (8) | `RESOURCE_EXHAUSTED` |
| `FailedPrecondition` (9) | `FAILED_PRECONDITION` |
| `Aborted` (10) | `ABORTED` |
| `OutOfRange` (11) | `OUT_OF_RANGE` |
| `Unimplemented` (12) | `UNIMPLEMENTED` |
| `Internal` (13) | `INTERNAL` |
| `Unavailable` (14) | `UNAVAILABLE` |
| `DataLoss` (15) | `DATA_LOSS` |
| `Unauthenticated` (16) | `UNAUTHENTICATED` |
| *(гипотетический будущий код gRPC)* | `INTERNAL` |

Под-причина (`REQUEST_BODY_MALFORMED`, `VALIDATION_FAILED`, `INVALID_CREDENTIALS`)
восстанавливается из `ErrorInfo.Metadata["reason"]`, если апстрим её положил.

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
| `SERIALIZATION_FAILURE` | `40001` | `UNAVAILABLE` *(или `ABORTED` — см. ниже)* |
| `DEADLOCK_DETECTED` | `40P01` | `UNAVAILABLE` *(или `ABORTED`)* |
| `QUERY_CANCELED` | `57014` | `DEADLINE_EXCEEDED` |
| всё остальное | — | `INTERNAL` |

`SERIALIZATION_FAILURE` / `DEADLOCK_DETECTED`: если в сервисе есть внешний retry-цикл
транзакции — логичнее `ABORTED`, сигнализируя «повтори всю транзакцию».
Если ретраев нет и клиент просто должен попробовать позже — `UNAVAILABLE`.
Значение по умолчанию в `pgerr.Map` — `UNAVAILABLE`; переопределяется на уровне сервиса.

`CANCELED`, `FAILED_PRECONDITION`, `RESOURCE_EXHAUSTED`, `OUT_OF_RANGE`, `UNIMPLEMENTED`,
`DATA_LOSS`, `UNKNOWN` из Postgres напрямую не выводятся — эти коды рождаются в service-слое
или при round-trip'е через gRPC. `QUERY_CANCELED` (`57014`) остаётся на `DEADLINE_EXCEEDED`;
если запрос отменён именно из-за `context.Canceled`, service-слой может переопределить на `CANCELED`.

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

Для составной валидации добавляются `reason` и `violations`:

```json
{
  "error": {
    "code": "INVALID_ARGUMENT",
    "reason": "VALIDATION_FAILED",
    "message": "validation failed",
    "violations": [
      { "field": "email", "message": "must be a valid email" },
      { "field": "age", "message": "must be >= 18" }
    ],
    "request_id": "0f9b7c2e-..."
  }
}
```

Поле `reason` присутствует только когда под-причина задана (`REQUEST_BODY_MALFORMED`,
`VALIDATION_FAILED`, `INVALID_CREDENTIALS`); `code` при этом остаётся каноничным.

gRPC: `status.New(code.GRPC(), err.Public())` + деталь
`errdetails.ErrorInfo{ Reason: "<CODE>", Domain: "delivery", Metadata: {"reason": "<SUB>"} }`
(ключ `reason` в `Metadata` — только если под-причина задана).

В лог при этом уходит полная цепочка причин (`error.code`, `error.reason`, `error.cause`)
на уровне `Code.Level()`.

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

// Имена совпадают с каноническими именами gRPC-кодов — Code <-> gRPC биективен по всем 16.
const (
	CodeCanceled           Code = "CANCELED"
	CodeUnknown            Code = "UNKNOWN"
	CodeInvalidArgument    Code = "INVALID_ARGUMENT"
	CodeDeadlineExceeded   Code = "DEADLINE_EXCEEDED"
	CodeNotFound           Code = "NOT_FOUND"
	CodeAlreadyExists      Code = "ALREADY_EXISTS"
	CodePermissionDenied   Code = "PERMISSION_DENIED"
	CodeResourceExhausted  Code = "RESOURCE_EXHAUSTED"
	CodeFailedPrecondition Code = "FAILED_PRECONDITION"
	CodeAborted            Code = "ABORTED"
	CodeOutOfRange         Code = "OUT_OF_RANGE"
	CodeUnimplemented      Code = "UNIMPLEMENTED"
	CodeInternal           Code = "INTERNAL"
	CodeUnavailable        Code = "UNAVAILABLE"
	CodeDataLoss           Code = "DATA_LOSS"
	CodeUnauthenticated    Code = "UNAUTHENTICATED"
)

// Под-причины: уточняют Code для клиента, на транспортный маппинг не влияют.
const (
	ReasonRequestBodyMalformed = "REQUEST_BODY_MALFORMED"
	ReasonValidationFailed     = "VALIDATION_FAILED"
	ReasonInvalidCredentials   = "INVALID_CREDENTIALS" //nolint:gosec // имя причины, не секрет
)

const (
	MessageInternal           = "internal server error"
	MessageInvalidArgument    = "invalid argument"
	MessageInvalidRequestBody = "invalid request body"
	MessageValidationFailed   = "validation failed"
	MessageNotFound           = "not found"
	MessageAlreadyExists      = "already exists"
	MessageAborted            = "operation aborted"
	MessageResourceExhausted  = "resource exhausted"
	MessageFailedPrecondition = "failed precondition"
	MessageOutOfRange         = "out of range"
	MessageUnimplemented      = "not implemented"
	MessageCanceled           = "request canceled"
	MessageUnauthenticated    = "unauthenticated"
	MessageInvalidCredentials = "invalid credentials" //nolint:gosec // текст ошибки, не секрет
	MessagePermissionDenied   = "permission denied"
	MessageDeadlineExceeded   = "deadline exceeded"
	MessageUnavailable        = "service unavailable"
)

// GRPC — доменный код -> gRPC-статус. Почти тождество: имена совпадают,
// решений здесь нет, только таблица.
func (c Code) GRPC() codes.Code {
	switch c {
	case CodeCanceled:
		return codes.Canceled
	case CodeUnknown:
		return codes.Unknown
	case CodeInvalidArgument:
		return codes.InvalidArgument
	case CodeDeadlineExceeded:
		return codes.DeadlineExceeded
	case CodeNotFound:
		return codes.NotFound
	case CodeAlreadyExists:
		return codes.AlreadyExists
	case CodePermissionDenied:
		return codes.PermissionDenied
	case CodeResourceExhausted:
		return codes.ResourceExhausted
	case CodeFailedPrecondition:
		return codes.FailedPrecondition
	case CodeAborted:
		return codes.Aborted
	case CodeOutOfRange:
		return codes.OutOfRange
	case CodeUnimplemented:
		return codes.Unimplemented
	case CodeUnavailable:
		return codes.Unavailable
	case CodeDataLoss:
		return codes.DataLoss
	case CodeUnauthenticated:
		return codes.Unauthenticated
	default:
		return codes.Internal
	}
}

// HTTP — доменный код -> HTTP-статус. Фиксированная таблица google.rpc.Code.
func (c Code) HTTP() int {
	switch c {
	case CodeInvalidArgument, CodeFailedPrecondition, CodeOutOfRange:
		return http.StatusBadRequest
	case CodeUnauthenticated:
		return http.StatusUnauthorized
	case CodePermissionDenied:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeAlreadyExists, CodeAborted:
		return http.StatusConflict
	case CodeResourceExhausted:
		return http.StatusTooManyRequests
	case CodeCanceled:
		return 499 // нет http-константы: "Client Closed Request" (расширение nginx)
	case CodeUnimplemented:
		return http.StatusNotImplemented
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	case CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	default: // INTERNAL, UNKNOWN, DATA_LOSS
		return http.StatusInternalServerError
	}
}

// Level — уровень лога для этого кода. Interceptor и HTTP-writer
// логируют единообразно, не решая уровень на каждом вызове.
func (c Code) Level() slog.Level {
	switch c {
	case CodeInternal, CodeUnknown, CodeDataLoss,
		CodeUnavailable, CodeDeadlineExceeded, CodeUnimplemented:
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
	case CodeNotFound:
		return MessageNotFound
	case CodeAlreadyExists:
		return MessageAlreadyExists
	case CodeAborted:
		return MessageAborted
	case CodeCanceled:
		return MessageCanceled
	case CodeResourceExhausted:
		return MessageResourceExhausted
	case CodeFailedPrecondition:
		return MessageFailedPrecondition
	case CodeOutOfRange:
		return MessageOutOfRange
	case CodeUnimplemented:
		return MessageUnimplemented
	case CodeUnauthenticated:
		return MessageUnauthenticated
	case CodePermissionDenied:
		return MessagePermissionDenied
	case CodeDeadlineExceeded:
		return MessageDeadlineExceeded
	case CodeUnavailable:
		return MessageUnavailable
	default: // INTERNAL, UNKNOWN, DATA_LOSS — generic-текст
		return MessageInternal
	}
}

// CodeFromGRPC — обратный маппинг для gateway. Тотальная биекция по всем 16 кодам;
// default -> INTERNAL оставлен только на случай будущих кодов gRPC.
func CodeFromGRPC(c codes.Code) Code {
	switch c {
	case codes.OK:
		return ""
	case codes.Canceled:
		return CodeCanceled
	case codes.Unknown:
		return CodeUnknown
	case codes.InvalidArgument:
		return CodeInvalidArgument
	case codes.DeadlineExceeded:
		return CodeDeadlineExceeded
	case codes.NotFound:
		return CodeNotFound
	case codes.AlreadyExists:
		return CodeAlreadyExists
	case codes.PermissionDenied:
		return CodePermissionDenied
	case codes.ResourceExhausted:
		return CodeResourceExhausted
	case codes.FailedPrecondition:
		return CodeFailedPrecondition
	case codes.Aborted:
		return CodeAborted
	case codes.OutOfRange:
		return CodeOutOfRange
	case codes.Unimplemented:
		return CodeUnimplemented
	case codes.Internal:
		return CodeInternal
	case codes.Unavailable:
		return CodeUnavailable
	case codes.DataLoss:
		return CodeDataLoss
	case codes.Unauthenticated:
		return CodeUnauthenticated
	default:
		return CodeInternal
	}
}
```

### 8.2 `platform/apperr/error.go`

```go
package apperr

import (
	"context"
	"errors"
	"log/slog"
)

// Error — доменная ошибка, единая для всех слоёв.
type Error struct {
	Code       Code        // машинный код, драйвит маппинг в транспорт
	Reason     string      // опциональная под-причина для клиента; транспорт не трогает
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

// publicByReason — фиксированный безопасный текст для под-причин у кодов, которые
// Message наружу не отдают. Точка расширения: новая сабричина со своим текстом —
// одна строка здесь.
var publicByReason = map[string]string{
	ReasonInvalidCredentials: MessageInvalidCredentials,
}

// Public — текст, который реально уходит клиенту.
// «Наружу без деталей» коды Message игнорируют: отдаётся generic-текст кода либо
// фиксированный текст под-причины из publicByReason.
func (e *Error) Public() string {
	switch e.Code {
	case CodeInternal, CodeUnknown, CodeDataLoss, CodeUnavailable,
		CodeDeadlineExceeded, CodeUnauthenticated:
		if m, ok := publicByReason[e.Reason]; ok { // e.Reason=="" -> ok==false
			return m
		}
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
	if e.Reason != "" {
		attrs = append(attrs, slog.String("reason", e.Reason))
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
	ErrCanceled           = &Error{Code: CodeCanceled}
	ErrUnknown            = &Error{Code: CodeUnknown}
	ErrInvalidArgument    = &Error{Code: CodeInvalidArgument}
	ErrDeadlineExceeded   = &Error{Code: CodeDeadlineExceeded}
	ErrNotFound           = &Error{Code: CodeNotFound}
	ErrAlreadyExists      = &Error{Code: CodeAlreadyExists}
	ErrPermissionDenied   = &Error{Code: CodePermissionDenied}
	ErrResourceExhausted  = &Error{Code: CodeResourceExhausted}
	ErrFailedPrecondition = &Error{Code: CodeFailedPrecondition}
	ErrAborted            = &Error{Code: CodeAborted}
	ErrOutOfRange         = &Error{Code: CodeOutOfRange}
	ErrUnimplemented      = &Error{Code: CodeUnimplemented}
	ErrInternal           = &Error{Code: CodeInternal}
	ErrUnavailable        = &Error{Code: CodeUnavailable}
	ErrDataLoss           = &Error{Code: CodeDataLoss}
	ErrUnauthenticated    = &Error{Code: CodeUnauthenticated}
)

// As — извлечь *Error из цепочки.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// From — нормализовать ЛЮБУЮ ошибку в *Error. Голый context.Canceled /
// context.DeadlineExceeded не должен становиться INTERNAL; всё прочее — fallback INTERNAL.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	if e, ok := As(err); ok {
		return e
	}
	switch {
	case errors.Is(err, context.Canceled):
		return &Error{Code: CodeCanceled, Err: err}
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Code: CodeDeadlineExceeded, Err: err}
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

// WithReason — задать под-причину. Чейнится.
func (e *Error) WithReason(reason string) *Error {
	e.Reason = reason
	return e
}

// WithViolations — добавить детали полей. Чейнится.
func (e *Error) WithViolations(v ...Violation) *Error {
	e.Violations = append(e.Violations, v...)
	return e
}

// Сахар по кодам: короткая и форматная версии. Только для кодов, чей Message
// реально уходит клиенту (Public() его отдаёт).
func InvalidArgument(msg string) *Error          { return New(CodeInvalidArgument, msg) }
func InvalidArgumentf(f string, a ...any) *Error { return New(CodeInvalidArgument, fmt.Sprintf(f, a...)) }

func NotFound(msg string) *Error          { return New(CodeNotFound, msg) }
func NotFoundf(f string, a ...any) *Error { return New(CodeNotFound, fmt.Sprintf(f, a...)) }

func AlreadyExists(msg string) *Error          { return New(CodeAlreadyExists, msg) }
func AlreadyExistsf(f string, a ...any) *Error { return New(CodeAlreadyExists, fmt.Sprintf(f, a...)) }

func Aborted(msg string) *Error          { return New(CodeAborted, msg) }
func Abortedf(f string, a ...any) *Error { return New(CodeAborted, fmt.Sprintf(f, a...)) }

func PermissionDenied(msg string) *Error          { return New(CodePermissionDenied, msg) }
func PermissionDeniedf(f string, a ...any) *Error { return New(CodePermissionDenied, fmt.Sprintf(f, a...)) }

func FailedPrecondition(msg string) *Error          { return New(CodeFailedPrecondition, msg) }
func FailedPreconditionf(f string, a ...any) *Error { return New(CodeFailedPrecondition, fmt.Sprintf(f, a...)) }

func ResourceExhausted(msg string) *Error          { return New(CodeResourceExhausted, msg) }
func ResourceExhaustedf(f string, a ...any) *Error { return New(CodeResourceExhausted, fmt.Sprintf(f, a...)) }

func OutOfRange(msg string) *Error          { return New(CodeOutOfRange, msg) }
func OutOfRangef(f string, a ...any) *Error { return New(CodeOutOfRange, fmt.Sprintf(f, a...)) }

func Unimplemented(msg string) *Error          { return New(CodeUnimplemented, msg) }
func Unimplementedf(f string, a ...any) *Error { return New(CodeUnimplemented, fmt.Sprintf(f, a...)) }

// «Наружу без деталей» — INTERNAL, UNKNOWN, DATA_LOSS, UNAVAILABLE, DEADLINE_EXCEEDED,
// UNAUTHENTICATED: Public() отдаёт generic-текст (либо текст под-причины из
// publicByReason), клиентского Message у них нет — поэтому и конструкторы без сообщения.
// Диагностику несёт .Wrap(err) (уходит в лог полем cause); произвольную ошибку в
// INTERNAL нормализует apperr.From(err). UNKNOWN вручную не конструируют вовсе — только
// CodeFromGRPC.
func Internal() *Error         { return &Error{Code: CodeInternal} }
func Unavailable() *Error      { return &Error{Code: CodeUnavailable} }
func DeadlineExceeded() *Error { return &Error{Code: CodeDeadlineExceeded} }
func DataLoss() *Error         { return &Error{Code: CodeDataLoss} }

// Unauthenticated — 401 без под-причины: нет / протух / битый токен.
func Unauthenticated() *Error { return &Error{Code: CodeUnauthenticated} }

// CANCELED конструктора тоже не имеет: приходит только из context / gRPC через
// apperr.From и CodeFromGRPC.

// Сахар по под-причинам INVALID_ARGUMENT / UNAUTHENTICATED.

// InvalidRequestBody — тело не удалось разобрать (не JSON, не тот тип, битый protobuf).
func InvalidRequestBody(msg string) *Error {
	return New(CodeInvalidArgument, msg).WithReason(ReasonRequestBodyMalformed)
}

// Validation — составная ошибка валидации со списком полей.
func Validation(v ...Violation) *Error {
	return New(CodeInvalidArgument, MessageValidationFailed).
		WithReason(ReasonValidationFailed).
		WithViolations(v...)
}

// InvalidCredentials — учётные данные предъявлены, но не совпали. Наружу — фиксированный
// "invalid credentials" (из publicByReason по сабричине), не раскрывает логин vs пароль.
// Message здесь — только для Error()/лога; Public() берёт текст из publicByReason.
func InvalidCredentials() *Error {
	return New(CodeUnauthenticated, MessageInvalidCredentials).WithReason(ReasonInvalidCredentials)
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
			return apperr.Unavailable().Wrap(err)
		case pgerrcode.QueryCanceled:
			return apperr.DeadlineExceeded().Wrap(err)
		}
	}
	return apperr.Internal().Wrap(err)
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
			slog.Any("error", e), // LogValue: code + reason + cause в логах
		)

		st := status.New(e.Code.GRPC(), e.Public()) // в сеть — только Public()

		errInfo := &errdetails.ErrorInfo{Reason: string(e.Code), Domain: domain}
		if e.Reason != "" {
			errInfo.Metadata = map[string]string{"reason": e.Reason}
		}

		// protoadapt.MessageV1 == proto.Message; errdetails.* его реализуют.
		details := []protoadapt.MessageV1{errInfo}
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
		return apperr.Internal().Wrap(err)
	}

	code := apperr.CodeFromGRPC(st.Code())
	var reason string
	var violations []apperr.Violation

	for _, d := range st.Details() {
		switch t := d.(type) {
		case *errdetails.ErrorInfo:
			if t.GetDomain() == domain {
				if r := t.GetReason(); r != "" {
					code = apperr.Code(r) // точный доменный код
				}
				reason = t.GetMetadata()["reason"]
			}
		case *errdetails.BadRequest:
			for _, fv := range t.GetFieldViolations() {
				violations = append(violations,
					apperr.Violation{Field: fv.GetField(), Message: fv.GetDescription()})
			}
		}
	}

	out := apperr.New(code, st.Message()).Wrap(err)
	if reason != "" {
		out.WithReason(reason)
	}
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
	Reason     string             `json:"reason,omitempty"`
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
		Reason:     e.Reason,
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
		// -> Code: INVALID_ARGUMENT, Reason: REQUEST_BODY_MALFORMED, HTTP 400
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
		return apperr.Validation(v...)
		// Code: INVALID_ARGUMENT, Reason: VALIDATION_FAILED
		// HTTP 400 / codes.InvalidArgument + BadRequest details
	}
	// ...
}
```
