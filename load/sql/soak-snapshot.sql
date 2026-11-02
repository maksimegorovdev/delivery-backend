-- Снимок состояния БД для soak: размеры таблиц, время запросов к ним, слот репликации Debezium и WAL.
-- Запускается в notification-postgres (inbox_events, notifications); order-postgres (outbox_events, слот, WAL) — через dblink.
-- Снимок делается в начале и в конце прогона, затем сравнивается по строкам таблицы «Soak» спеки.
--
-- Параметры (psql -v):
--   order_dsn  строка подключения dblink к БД order
--   label      подпись снимка (start | end)
CREATE EXTENSION IF NOT EXISTS dblink;

\pset footer off
\pset null '—'
\echo 'Снимок soak:' :label

\echo 'Размеры таблиц (pg_total_relation_size) и число строк'
SELECT * FROM (
  SELECT 'outbox_events' AS "table", 'order' AS db, t.bytes, pg_size_pretty(t.bytes) AS size, t.rows
  FROM dblink(:'order_dsn',
         'SELECT pg_total_relation_size(''outbox_events''), (SELECT count(*) FROM outbox_events)')
       AS t(bytes bigint, rows bigint)
  UNION ALL
  SELECT 'inbox_events', 'notification', pg_total_relation_size('inbox_events'),
         pg_size_pretty(pg_total_relation_size('inbox_events')), (SELECT count(*) FROM inbox_events)
  UNION ALL
  SELECT 'notifications', 'notification', pg_total_relation_size('notifications'),
         pg_size_pretty(pg_total_relation_size('notifications')), (SELECT count(*) FROM notifications)
) s ORDER BY "table";

-- Время типичного запроса к таблице: подсчёт строк (растёт вместе с размером). Для outbox_events
-- время включает вызов dblink; берётся минимум из трёх повторов, чтобы убрать разовые выбросы кэша.
\echo 'Время count(*), мс (минимум из 3)'
SELECT set_config('soak.order_dsn', :'order_dsn', false) AS _ \gset
DO $$
DECLARE
  t0 timestamptz;
  best numeric;
  d numeric;
  res jsonb := '{}';
  tbl text;
  n bigint;
BEGIN
  FOREACH tbl IN ARRAY ARRAY['inbox_events', 'notifications'] LOOP
    best := NULL;
    FOR i IN 1..3 LOOP
      t0 := clock_timestamp();
      EXECUTE format('SELECT count(*) FROM %I', tbl) INTO n;
      d := extract(epoch FROM clock_timestamp() - t0) * 1000;
      best := least(coalesce(best, d), d);
    END LOOP;
    res := res || jsonb_build_object(tbl, round(best, 2));
  END LOOP;
  best := NULL;
  FOR i IN 1..3 LOOP
    t0 := clock_timestamp();
    PERFORM * FROM dblink(current_setting('soak.order_dsn'), 'SELECT count(*) FROM outbox_events') AS x(c bigint);
    d := extract(epoch FROM clock_timestamp() - t0) * 1000;
    best := least(coalesce(best, d), d);
  END LOOP;
  res := res || jsonb_build_object('outbox_events', round(best, 2));
  PERFORM set_config('soak.timing', res::text, false);
END $$;
SELECT key AS "table", value AS count_ms FROM jsonb_each_text(current_setting('soak.timing')::jsonb) ORDER BY key;

\echo 'Слот репликации Debezium и WAL в order-postgres'
SELECT t.*
FROM dblink(:'order_dsn', $q$
  SELECT slot_name, active, confirmed_flush_lsn::text, pg_current_wal_lsn()::text AS current_wal_lsn,
         pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn)::bigint AS flush_lag_bytes,
         pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)::bigint AS retained_wal_bytes,
         (SELECT coalesce(sum(size), 0) FROM pg_ls_waldir())::bigint AS wal_dir_bytes
  FROM pg_replication_slots
$q$) AS t(slot_name text, active boolean, confirmed_flush_lsn text, current_wal_lsn text,
          flush_lag_bytes bigint, retained_wal_bytes bigint, wal_dir_bytes bigint);
