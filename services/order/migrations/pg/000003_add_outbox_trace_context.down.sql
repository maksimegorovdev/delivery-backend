SET lock_timeout = '5s';

ALTER TABLE outbox_events
    DROP COLUMN tracestate,
    DROP COLUMN traceparent;
