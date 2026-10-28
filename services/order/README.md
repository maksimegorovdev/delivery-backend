# Order Service

Сервис заказов.

## Настройки

Все настройки находятся в `.env`.

Шаблон для `.env` находится в `.env.example`.

## Команды

Все команды описаны в `Taskfile.yml`.

Посмотреть список можно через `task --list`.

## Выкатка trace context в outbox (миграция 000003)

Миграция `000003`, код order и конфиг коннектора Debezium зависят друг от друга:

- новый `OutboxRepo.Save` пишет колонки `traceparent`/`tracestate`, и без миграции ломается каждый INSERT в outbox;
- конфиг коннектора ссылается на эти колонки в `table.fields.additional.placement`; поведение EventRouter
  при отсутствующей колонке не проверялось, полагаться на него нельзя.

Порядок выкатки:

1. `task migrate-up` — колонки nullable, без DEFAULT; старый код продолжает работать.
2. Выкатить код order.
3. Убедиться, что коннектор `order-outbox` в статусе RUNNING и вычитал строки, записанные до миграции:
   `curl -s http://localhost:8083/connectors/order-outbox/status`.
4. Из корня репозитория: `task connect-register`.

Откат — в обратном порядке:

1. Применить конфиг коннектора без `traceparent`/`tracestate` (`deploy/debezium/order-outbox.json` из предыдущей
   версии) через `task connect-register`.
2. Выкатить предыдущую версию кода order.
3. Откатить только эту миграцию: `bin/migrate -database "$PG_DSN" -path migrations/pg down 1`.
   `task migrate-down` для этого не подходит — он откатывает все миграции.

Шаг 3 необязателен: пустые nullable-колонки не мешают старому коду.

Миграция ставит `lock_timeout = 5s`: `ALTER TABLE` берёт ACCESS EXCLUSIVE, и без таймаута под нагрузкой
встал бы в очередь за долгими транзакциями, блокируя все запросы к `outbox_events`. При ошибке таймаута
изменения не применяются, но migrate помечает версию как dirty: перед повтором вернуть версию
(`bin/migrate -database "$PG_DSN" -path migrations/pg force 2`) и снова запустить `task migrate-up`.
