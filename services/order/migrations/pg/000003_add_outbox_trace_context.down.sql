ALTER TABLE outbox_events
    DROP COLUMN tracestate,
    DROP COLUMN traceparent;
