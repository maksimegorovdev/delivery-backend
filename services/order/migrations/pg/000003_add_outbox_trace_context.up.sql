SET lock_timeout = '5s';

ALTER TABLE outbox_events
    ADD COLUMN traceparent TEXT,
    ADD COLUMN tracestate  TEXT;
