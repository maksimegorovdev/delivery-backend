# Order Service — реализация CreateOrder

Практическое направление: как в `order`-сервисе работать с несколькими таблицами,
репозиториями и usecase, отталкиваясь от того, как это уже сделано в `user`-сервисе.

Документ описывает **реальную (плоскую) раскладку**, как в коде `user`-сервиса
(`internal/{domain,usecase,repository/pg,transport/grpc}`), а не aspirational-структуру
из `architecture.md` (`ports/`, `adapters/inbound|outbound`, `txmanager`, `outbox`).
Идея слоёв та же, имена каталогов — как в текущем коде.

---

## 1. Что принципиально иначе, чем в user-сервисе

`user` тривиальный: одна таблица = один репозиторий = один `GetByID`.
В `order.CreateOrder` появляются три новые вещи:

1. **Агрегат.** `Order` владеет `[]OrderItem`. Это одна единица целостности, а не две
   независимые сущности. Значит один репозиторий `OrderRepo` отвечает за обе таблицы
   (`orders` + `order_items`), а не два репозитория.
2. **Транзакция.** Вставка в две таблицы должна быть атомарной.
3. **Внешние данные.** В `CreateOrderRequest` только `user_id`, `address_id` и
   `product_id`+`quantity`, а в БД лежат `delivery_address TEXT`,
   `order_items.product_name`, `order_items.unit_price`. Их надо откуда-то взять:
   пользователь и адрес — из `user`-сервиса (`UserService.GetUser`,
   `AddressService.GetAddress`), товары — из каталога (сервиса пока нет).
   Это порты, которые объявляет usecase.

Поток:

```
grpc.CreateOrder(proto)
  → transport мапит proto → usecase.CreateOrderInput
  → usecase:  GetUser(user_id)        — проверка существования (порт), ДО транзакции
              GetAddress(address_id)  — резолв адреса доставки (порт)
              GetProducts(ids)        — обогащение позиций name/price (порт)
           → domain.NewOrder(...)     — инварианты + расчёт total
           → OrderRepo.Create(order)  — порт
  → repository/pg: BEGIN → INSERT orders → INSERT order_items → COMMIT
  → transport мапит domain.Order → proto
```

---

## 2. domain — агрегат и бизнес-правила

`internal/domain/order.go`:

```go
package domain

import "time"

type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusConfirmed OrderStatus = "confirmed"
	OrderStatusCancelled OrderStatus = "cancelled"
)

type OrderItem struct {
	ID          string
	OrderID     string
	ProductID   string
	ProductName string
	Quantity    int32
	UnitPrice   int64 // копейки
}

func (i OrderItem) Subtotal() int64 { return i.UnitPrice * int64(i.Quantity) }

type Order struct {
	ID              string
	UserID          string
	Status          OrderStatus
	Items           []OrderItem
	TotalAmount     int64 // копейки
	DeliveryAddress string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
```

Фабрика в домене держит инварианты (непустой список, стартовый статус, расчёт суммы) —
usecase не должен считать `total` руками:

```go
// internal/domain/order.go
func NewOrder(userID, deliveryAddress string, items []OrderItem) (Order, error) {
	if len(items) == 0 {
		return Order{}, ErrOrderNoItems
	}
	var total int64
	for _, it := range items {
		total += it.Subtotal()
	}
	return Order{
		UserID:          userID,
		Status:          OrderStatusPending,
		Items:           items,
		TotalAmount:     total,
		DeliveryAddress: deliveryAddress,
	}, nil
}
```

Доменные ошибки рядом (`internal/domain/errors.go`) — отдельными значениями, без импорта
transport/pg. Маппинг в gRPC-код делает `interceptors.Error()` через `apperr`:

```go
package domain

import "errors"

var (
	ErrOrderNoItems    = errors.New("order: no items")
	ErrProductNotFound = errors.New("order: product not found")
)
```

Маленькие доменные структуры для портов (не proto):

```go
// internal/domain/user.go — то, что order-у нужно от user-сервиса
type User struct {
	ID    string
	Email string
}

// internal/domain/address.go
type Address struct {
	ID      string
	UserID  string
	Address string
}

// internal/domain/product.go
type Product struct {
	ID    string
	Name  string
	Price int64 // копейки
}
```

Домен не импортирует `proto`, `pgx`, `grpc`. Только stdlib и `apperr` (как в `platform`).

---

## 3. Порты — интерфейсы объявляет потребитель (usecase)

Как в `user`-сервисе (`UserRepo` живёт в пакете `usecase`), но портов теперь несколько.
`internal/usecase/order.go`:

```go
package usecase

import (
	"context"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

// запись агрегата
type OrderRepo interface {
	Create(ctx context.Context, o domain.Order) (domain.Order, error)
}

// всё, что order-у нужно от user-сервиса.
// GetUser и GetAddress — в одном порту, потому что это одна подсистема,
// один жизненный цикл, всегда вместе.
type UserProvider interface {
	GetUser(ctx context.Context, id string) (domain.User, error)
	GetAddress(ctx context.Context, id string) (domain.Address, error)
}

// каталог товаров: сервиса пока нет.
// Реализация — статическая заглушка; интерфейс уже правильный,
// потом подменяешь на gRPC-клиент, usecase не меняется.
type ProductProvider interface {
	GetProducts(ctx context.Context, ids []string) (map[string]domain.Product, error)
}
```

Ключевая мысль: usecase не знает, что `UserProvider` — это gRPC, а `OrderRepo` —
Postgres. Он видит только «дай юзера / адрес / товары» и «сохрани заказ».

### Имя порта vs имя реализации

Это **разные имена**, и это норма, а не дублирование:

| | Что это | Где живёт | Как называется | Почему |
|---|---|---|---|---|
| Порт | интерфейс, **потребность** usecase | `usecase` | `UserProvider` | по способности; не `UserServiceClient` — транспорт и чужой сервис не должны течь в usecase |
| Адаптер | реализация, знает про gRPC | `client` | `UserClient` | по тому, **что это** физически |

Пакет реализации — `client`. Не `gateway` (это уже имя сервиса в проекте), не `adapter`.

---

## 4. usecase — оркестрация

`internal/usecase/order.go` (продолжение). Вход — свой DTO уровня usecase, не proto и не
domain:

```go
type CreateOrderInput struct {
	UserID    string
	AddressID string
	Items     []CreateOrderItemInput
}

type CreateOrderItemInput struct {
	ProductID string
	Quantity  int32
}

type OrderUsecase struct {
	orders   OrderRepo
	users    UserProvider
	products ProductProvider
}

func NewOrderUsecase(orders OrderRepo, users UserProvider, products ProductProvider) *OrderUsecase {
	return &OrderUsecase{orders: orders, users: users, products: products}
}

func (uc *OrderUsecase) CreateOrder(ctx context.Context, in CreateOrderInput) (domain.Order, error) {
	// 1. внешние данные — ДО построения агрегата и ДО транзакции

	// существование пользователя (NotFound прилетит из провайдера как apperr)
	if _, err := uc.users.GetUser(ctx, in.UserID); err != nil {
		return domain.Order{}, err
	}
	// когда в user.proto появится поле status — здесь же проверка active

	addr, err := uc.users.GetAddress(ctx, in.AddressID)
	if err != nil {
		return domain.Order{}, err
	}

	ids := make([]string, len(in.Items))
	for i, it := range in.Items {
		ids[i] = it.ProductID
	}
	catalog, err := uc.products.GetProducts(ctx, ids)
	if err != nil {
		return domain.Order{}, err
	}

	// 2. обогащаем позиции данными каталога
	items := make([]domain.OrderItem, 0, len(in.Items))
	for _, it := range in.Items {
		p, ok := catalog[it.ProductID]
		if !ok {
			return domain.Order{}, apperr.NotFound().Wrap(domain.ErrProductNotFound)
		}
		items = append(items, domain.OrderItem{
			ProductID:   p.ID,
			ProductName: p.Name,
			Quantity:    it.Quantity,
			UnitPrice:   p.Price,
		})
	}

	// 3. агрегат: инварианты + расчёт суммы внутри домена
	order, err := domain.NewOrder(in.UserID, addr.Address, items)
	if err != nil {
		return domain.Order{}, apperr.InvalidArgument().Wrap(err)
	}

	// 4. запись (транзакция внутри репозитория)
	return uc.orders.Create(ctx, order)
}
```

Транзакции здесь нет — она внутри `orders.Create`. Usecase про БД ничего не знает.
Внешние вызовы — **до** записи: держать соединение user-сервиса открытым внутри
транзакции orderdb — антипаттерн.

---

## 5. repository/pg — один репозиторий, две таблицы, транзакция

### 5.1 Абстракция над pool и tx

Чтобы приватные методы работали и на `*pgxpool.Pool`, и на `pgx.Tx`.
`internal/repository/pg/db.go`:

```go
package pg

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}
```

И `*pgxpool.Pool`, и `pgx.Tx` этот интерфейс уже удовлетворяют.

### 5.2 Публичный метод открывает транзакцию

`internal/repository/pg/order.go`:

```go
package pg

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr/pgerr"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

type OrderRepo struct {
	pool *pgxpool.Pool
}

func NewOrderRepo(pool *pgxpool.Pool) *OrderRepo { return &OrderRepo{pool: pool} }

func (r *OrderRepo) Create(ctx context.Context, o domain.Order) (domain.Order, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Order{}, pgerr.Map(err)
	}
	defer tx.Rollback(ctx) // no-op после Commit

	created, err := r.insertOrder(ctx, tx, o)
	if err != nil {
		return domain.Order{}, err
	}

	created.Items, err = r.insertItems(ctx, tx, created.ID, o.Items)
	if err != nil {
		return domain.Order{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Order{}, pgerr.Map(err)
	}
	return created, nil
}
```

### 5.3 Строки и мапперы — как userRow/addressRow

Тот же стиль: `db:"..."` теги + `pgx.RowToStructByName` + `toDomain()`.
Генерённые поля (`id`, `created_at`, `status default`) забираем через `RETURNING`:

```go
type orderRow struct {
	ID              string    `db:"id"`
	UserID          string    `db:"user_id"`
	Status          string    `db:"status"`
	TotalAmount     int64     `db:"total_amount"`
	DeliveryAddress string    `db:"delivery_address"`
	CreatedAt       time.Time `db:"created_at"`
	UpdatedAt       time.Time `db:"updated_at"`
}

func (r orderRow) toDomain() domain.Order {
	return domain.Order{
		ID:              r.ID,
		UserID:          r.UserID,
		Status:          domain.OrderStatus(r.Status), // строка БД → доменный enum
		TotalAmount:     r.TotalAmount,
		DeliveryAddress: r.DeliveryAddress,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

func (r *OrderRepo) insertOrder(ctx context.Context, q querier, o domain.Order) (domain.Order, error) {
	rows, err := q.Query(ctx,
		`INSERT INTO orders (user_id, status, total_amount, delivery_address)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, user_id, status, total_amount, delivery_address, created_at, updated_at`,
		o.UserID, string(o.Status), o.TotalAmount, o.DeliveryAddress,
	)
	if err != nil {
		return domain.Order{}, pgerr.Map(err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[orderRow])
	if err != nil {
		return domain.Order{}, pgerr.Map(err)
	}
	return row.toDomain(), nil
}
```

### 5.4 Позиции: цикл или батч

`internal/repository/pg/order_item.go` — `orderItemRow` + `toDomain()` аналогично.
Вставка — простой цикл `Query` (читается легко):

```go
func (r *OrderRepo) insertItems(ctx context.Context, q querier, orderID string, items []domain.OrderItem) ([]domain.OrderItem, error) {
	out := make([]domain.OrderItem, 0, len(items))
	for _, it := range items {
		rows, err := q.Query(ctx,
			`INSERT INTO order_items (order_id, product_id, product_name, quantity, unit_price)
			 VALUES ($1, $2, $3, $4, $5)
			 RETURNING id, order_id, product_id, product_name, quantity, unit_price`,
			orderID, it.ProductID, it.ProductName, it.Quantity, it.UnitPrice,
		)
		if err != nil {
			return nil, pgerr.Map(err)
		}
		row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[orderItemRow])
		if err != nil {
			return nil, pgerr.Map(err)
		}
		out = append(out, row.toDomain())
	}
	return out, nil
}
```

Оптимизация на потом — один `pgx.Batch` (1 round-trip вместо N):

```go
b := &pgx.Batch{}
for _, it := range items {
	b.Queue(`INSERT INTO order_items (...) VALUES ($1,$2,$3,$4,$5) RETURNING ...`, ...)
}
br := q.(interface{ SendBatch(context.Context, *pgx.Batch) pgx.BatchResults }).SendBatch(ctx, b)
defer br.Close()
// для каждой позиции: br.Query() → CollectExactlyOneRow
```

(если пойдёшь этим путём — добавь `SendBatch` в интерфейс `querier`, оба типа его имеют.)

### 5.5 Где держать транзакцию — решение

Пока один агрегат и одна операция записи — **транзакция внутри репозитория** (как выше),
usecase остаётся чистым. Это соответствует минимализму `user`-сервиса.

Если позже появится сага / запись в несколько агрегатов из usecase в одной транзакции —
вынести в `platform/txmanager` с `Do(ctx, func(ctx) error)`, где `ctx` несёт `pgx.Tx`, а
репозитории достают executor из контекста. **Сейчас не делать** — преждевременно.

---

## 6. transport/grpc — router + маппинг proto

Раскладка как в `user`: `router.go` + `order.go`.

`internal/transport/grpc/router.go`:

```go
type RouterDeps struct {
	Server       *grpc.Server
	OrderUsecase OrderUsecase
}

func NewRouter(deps *RouterDeps) {
	NewOrderRoutes(deps.Server, deps.OrderUsecase)
}
```

`internal/transport/grpc/order.go`:

```go
package grpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	orderv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/order/v1"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/usecase"
)

// интерфейс потребителя объявлен здесь (как UserUsecase в user-сервисе).
// вход тянем из пакета usecase — вход структурный, примитивами не отделаться.
type OrderUsecase interface {
	CreateOrder(ctx context.Context, in usecase.CreateOrderInput) (domain.Order, error)
}

type OrderRouter struct {
	orderv1.UnimplementedOrderServiceServer
	uc OrderUsecase
}

func NewOrderRoutes(server *grpc.Server, uc OrderUsecase) {
	orderv1.RegisterOrderServiceServer(server, &OrderRouter{uc: uc})
}

func (r *OrderRouter) CreateOrder(ctx context.Context, req *orderv1.CreateOrderRequest) (*orderv1.CreateOrderResponse, error) {
	in := usecase.CreateOrderInput{
		UserID:    req.GetUserId(),
		AddressID: req.GetAddressId(),
		Items:     make([]usecase.CreateOrderItemInput, 0, len(req.GetItems())),
	}
	for _, it := range req.GetItems() {
		in.Items = append(in.Items, usecase.CreateOrderItemInput{
			ProductID: it.GetProductId(),
			Quantity:  it.GetQuantity(),
		})
	}

	order, err := r.uc.CreateOrder(ctx, in)
	if err != nil {
		return nil, err // interceptors.Error() превратит apperr в gRPC status
	}
	return &orderv1.CreateOrderResponse{Order: orderToProto(order)}, nil
}
```

Мапперы `domain.Order → orderv1.Order` в этом же файле (как `userToProto`), + хелпер для
enum статуса:

```go
func statusToProto(s domain.OrderStatus) orderv1.OrderStatus {
	switch s {
	case domain.OrderStatusPending:
		return orderv1.OrderStatus_ORDER_STATUS_PENDING
	case domain.OrderStatusConfirmed:
		return orderv1.OrderStatus_ORDER_STATUS_CONFIRMED
	case domain.OrderStatusCancelled:
		return orderv1.OrderStatus_ORDER_STATUS_CANCELLED
	default:
		return orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}

func orderToProto(o domain.Order) *orderv1.Order {
	items := make([]*orderv1.OrderItem, 0, len(o.Items))
	for _, it := range o.Items {
		items = append(items, &orderv1.OrderItem{
			ProductId:   it.ProductID,
			ProductName: it.ProductName,
			Quantity:    it.Quantity,
			UnitPrice:   it.UnitPrice,
		})
	}
	return &orderv1.Order{
		Id:              o.ID,
		UserId:          o.UserID,
		Status:          statusToProto(o.Status),
		Items:           items,
		TotalAmount:     o.TotalAmount,
		DeliveryAddress: o.DeliveryAddress,
		CreatedAt:       timestamppb.New(o.CreatedAt),
		UpdatedAt:       timestamppb.New(o.UpdatedAt),
	}
}
```

Синтаксическую валидацию (`uuid`, `quantity > 0`, `min_items = 1`) уже делает
`interceptors.Validation` по правилам из `order_service.proto` — в хендлере не дублировать.

---

## 7. proto — что делить, что объединять

`GetUser` и `GetAddress` физически в одном `user`-сервисе, но остаются **раздельными
gRPC-контрактами** (`user.v1.UserService`, `address.v1.AddressService`) — так уже сделано,
менять не нужно.

| Уровень | Разделено или слито | Почему |
|---|---|---|
| **proto** (`.proto`, service) | **разделено** — `UserService` + `AddressService` | границы предметной области; `Address` — своя сущность (у юзера много адресов) |
| **сервер** (`grpc.Server`) | один на оба | gRPC мультиплексирует сервисы: `user/internal/transport/grpc/router.go` регистрирует оба на одном `Server` |
| **клиент** (order) | слито — один `UserClient`, один порт `UserProvider` | оба зова идут в один бинарь; удобство консьюмера |

Принцип: **контракт делишь по смыслу, адаптер клиента объединяешь по удобству.**

Профит от раздельных контрактов: когда/если адреса уедут в отдельный `address`-сервис —
контракт `AddressService` не меняется, консьюмеры перенастраивают только адрес соединения.

---

## 8. client — адаптер user-сервиса под порт usecase

Новый пакет `internal/client`. Одно gRPC-соединение к `user`-сервису → два
сгенерированных стаба из него. `internal/client/user.go`:

```go
package client

import (
	"context"

	"google.golang.org/grpc"

	addressv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/address/v1"
	userv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/user/v1"
	"github.com/maksimegorovdev/delivery-backend/platform/apperr/grpcerr"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

type UserClient struct {
	users     userv1.UserServiceClient
	addresses addressv1.AddressServiceClient
}

func NewUserClient(conn *grpc.ClientConn) *UserClient {
	return &UserClient{
		users:     userv1.NewUserServiceClient(conn),
		addresses: addressv1.NewAddressServiceClient(conn),
	}
}

func (u *UserClient) GetUser(ctx context.Context, id string) (domain.User, error) {
	resp, err := u.users.GetUser(ctx, &userv1.GetUserRequest{Id: id})
	if err != nil {
		return domain.User{}, grpcerr.Map(err) // status → apperr
	}
	x := resp.GetUser()
	return domain.User{ID: x.GetId(), Email: x.GetEmail()}, nil
}

func (u *UserClient) GetAddress(ctx context.Context, id string) (domain.Address, error) {
	resp, err := u.addresses.GetAddress(ctx, &addressv1.GetAddressRequest{Id: id})
	if err != nil {
		return domain.Address{}, grpcerr.Map(err)
	}
	x := resp.GetAddress()
	return domain.Address{ID: x.GetId(), UserID: x.GetUserId(), Address: x.GetAddress()}, nil
}
```

Обёртка держит proto **на краю**: `usecase` не импортирует `userv1` / `addressv1`,
перевод `status.Error → apperr` — в одном месте, домен получает только нужные поля,
тесты мокают `UserProvider` (2 метода), а не весь сгенерированный клиент.

`ProductProvider` пока — статическая заглушка, `internal/client/product.go`:

```go
type StaticProductProvider struct {
	items map[string]domain.Product // из конфига или прямо в коде
}

func (p *StaticProductProvider) GetProducts(ctx context.Context, ids []string) (map[string]domain.Product, error) {
	out := make(map[string]domain.Product, len(ids))
	for _, id := range ids {
		pr, ok := p.items[id]
		if !ok {
			return nil, apperr.NotFound().Wrap(domain.ErrProductNotFound)
		}
		out[id] = pr
	}
	return out, nil
}
```

Появится product-сервис — заменишь на `ProductClient` по образцу `UserClient`.

---

## 9. app.go — проводка

Сейчас `order/internal/app/app.go` не создаёт ни репозиториев, ни usecase, ни роутера.
Добавляешь по образцу `user/internal/app/app.go`, плюс одно gRPC-соединение к user-сервису:

```go
// ... после создания pgPool и grpcServer ...

// gRPC client → user service (UserService + AddressService на одном conn)
userConn, err := grpc.NewClient(
	cfg.UserService.Addr,
	grpc.WithTransportCredentials(insecure.NewCredentials()),
)
if err != nil {
	return nil, err
}

// Repository
orderRepo := repo.NewOrderRepo(pgPool)

// Providers
userClient      := client.NewUserClient(userConn)
productProvider := client.NewStaticProductProvider(cfg.Catalog) // заглушка

// Usecase
orderUsecase := usecase.NewOrderUsecase(orderRepo, userClient, productProvider)

// Router
grpcrouter.NewRouter(&grpcrouter.RouterDeps{
	Server:       grpcServer.Server(),
	OrderUsecase: orderUsecase,
})

app := &App{
	cfg:        cfg,
	log:        log,
	pgPool:     pgPool,
	grpcServer: grpcServer,
	userConn:   userConn, // закрыть в Close()
}
```

`userClient` передаётся в usecase **один раз** — один порт `UserProvider`.
В `App.Close()` добавить `a.userConn.Close()` перед `a.pgPool.Close()`.
В `config.Config` добавить `UserService.Addr` (и данные каталога, пока заглушка).

---

## 10. Итоговое дерево (order)

```
services/order/internal/
  app/app.go                    # composition root: +orderRepo +userClient +productProvider
                                #   +usecase +router +userConn (закрыть в Close)
  config/config.go              # +UserService.Addr
  domain/
    order.go                    # Order, OrderItem, OrderStatus, NewOrder (инварианты + total)
    user.go  address.go  product.go   # маленькие структуры для портов
    errors.go                   # ErrOrderNoItems, ErrProductNotFound
  usecase/
    order.go                    # OrderUsecase + порты OrderRepo/UserProvider/ProductProvider
                                #   + CreateOrderInput / CreateOrderItemInput
  repository/pg/
    db.go                       # querier (pool | tx)
    order.go                    # OrderRepo.Create (BEGIN/COMMIT) + orderRow + insertOrder
    order_item.go               # orderItemRow + insertItems
  transport/grpc/
    router.go                   # RouterDeps + NewRouter
    order.go                    # OrderRouter + OrderUsecase iface + orderToProto/statusToProto
  client/
    user.go                     # UserClient (userv1 + addressv1 → domain) реализует UserProvider
    product.go                  # StaticProductProvider (заглушка) → позже ProductClient
```

Отличие от `architecture.md`: там `ports/` отдельным пакетом и `adapters/inbound|outbound`.
Здесь — как в текущем коде `user`-сервиса: интерфейсы объявлены рядом с потребителем
(`usecase`, `transport/grpc`), каталоги плоские. Когда/если проект дорастёт до
`txmanager` + `outbox`, дерево можно подвинуть к варианту из `architecture.md`.

---

## 11. Если адрес станет отдельным сервисом

Тогда, и только тогда — механическая правка на ~10 минут:

1. Режешь порт: `UserProvider` (`GetUser`) + `AddressProvider` (`GetAddress`).
2. Режешь клиент: `client.UserClient` + `client.AddressClient` (разные `conn`).
3. Правишь `app.go`: два параметра вместо одного.

`usecase.CreateOrder` и `domain` не трогаешь. Ради этого порт и назван по потребности,
а не `UserServiceClient`. **Закладываться сейчас не нужно.**

---

## 12. Порядок реализации

1. `domain`: `order.go` (`Order`, `OrderItem`, `OrderStatus`, `NewOrder`), `errors.go`,
   маленькие `user.go` / `address.go` / `product.go`.
2. `usecase/order.go`: порты + `CreateOrderInput` + `OrderUsecase.CreateOrder`
   (пока с заглушками провайдеров — компилируется и тестируется на моках).
3. `repository/pg`: `db.go`, `order.go`, `order_item.go`. Проверить `Create` на живой БД.
4. `client/user.go`: `UserClient` над `userv1` + `addressv1`.
   `client/product.go`: статическая заглушка.
5. `transport/grpc`: `router.go`, `order.go` + мапперы.
6. `app/app.go` + `config`: проводка, `userConn`, закрытие в `Close()`.
7. Проверить сквозь `grpcurl` / gateway: `CreateOrder` → строки в `orders` + `order_items`.

---

## 13. Мелочь мимоходом

В `services/order/migrations/pg/000001_create_orders_tables.down.sql` порядок `DROP`
обратный: `orders` дропается раньше `order_items`, а FK `fk_order_items_order_id` это
уронит. Поменять местами (сначала `order_items`) либо `DROP TABLE ... CASCADE`.
