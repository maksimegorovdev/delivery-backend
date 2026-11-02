-- Лаг асинхронной цепочки outbox -> Kafka -> notification по ступеням прогона.
-- Запускается в notification-postgres; заказы берутся из order-postgres через dblink.
-- Лаг заказа = notifications.created_at - orders.created_at; связь: orders.id = inbox_events.aggregate_id,
-- inbox_events.event_id = notifications.event_id.
--
-- Параметры (psql -v):
--   order_dsn  строка подключения dblink к БД order
--   summary    содержимое summary.json прогона stress или пустая строка (тогда одна ступень 0 = окно сэмплера)
--   grouplag   содержимое group-lag.csv (ts,total_lag) или пустая строка
CREATE EXTENSION IF NOT EXISTS dblink;

\pset footer off
\pset null '—'

CREATE TEMP TABLE samples AS
SELECT split_part(l, ',', 1)::timestamptz AS ts, split_part(l, ',', 2)::bigint AS total_lag
FROM string_to_table(:'grouplag', E'\n') AS l
WHERE l ~ '^\d';

CREATE TEMP TABLE stages AS
SELECT (e ->> 'stage')::int AS stage, (e ->> 'target_rps')::numeric AS target_rps,
       (e ->> 'plateau_started_at')::timestamptz AS from_ts, (e ->> 'ended_at')::timestamptz AS to_ts
FROM jsonb_array_elements(NULLIF(:'summary', '')::jsonb -> 'stages') AS e
WHERE (e ->> 'reached')::boolean
UNION ALL
SELECT 0, NULL, min(ts), max(ts) + interval '10 seconds' FROM samples WHERE NULLIF(:'summary', '') IS NULL HAVING count(*) > 0;

CREATE TEMP TABLE order_lag AS
SELECT s.stage, o.id AS order_id, n.created_at - o.created_at AS lag
FROM dblink(:'order_dsn', 'SELECT id, created_at FROM orders') AS o(id uuid, created_at timestamptz)
JOIN stages s ON o.created_at >= s.from_ts AND o.created_at < s.to_ts
LEFT JOIN inbox_events i ON i.aggregate_id = o.id
LEFT JOIN notifications n ON n.event_id = i.event_id;

\echo 'Лаг заказ -> уведомление по ступеням (плато), секунды'
SELECT s.stage, s.target_rps,
       count(l.order_id) AS orders,
       count(l.lag) AS notified,
       count(l.order_id) - count(l.lag) AS missing,
       round(extract(epoch FROM percentile_cont(0.5) WITHIN GROUP (ORDER BY l.lag))::numeric, 3) AS p50_s,
       round(extract(epoch FROM percentile_cont(0.95) WITHIN GROUP (ORDER BY l.lag))::numeric, 3) AS p95_s,
       round(extract(epoch FROM max(l.lag))::numeric, 3) AS max_s
FROM stages s
LEFT JOIN order_lag l USING (stage)
GROUP BY s.stage, s.target_rps
ORDER BY s.stage;

\echo 'Лаг группы notification (rpk group describe), сообщения'
SELECT s.stage, s.target_rps,
       count(m.ts) AS samples,
       max(m.total_lag) AS max_lag,
       (SELECT m2.total_lag FROM samples m2 WHERE m2.ts <= s.to_ts ORDER BY m2.ts DESC LIMIT 1) AS lag_at_end
FROM stages s
LEFT JOIN samples m ON m.ts >= s.from_ts AND m.ts <= s.to_ts
GROUP BY s.stage, s.target_rps, s.to_ts
ORDER BY s.stage;
