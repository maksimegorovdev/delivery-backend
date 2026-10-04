CREATE TABLE inbox_events (
    event_id     UUID PRIMARY KEY,
    topic        TEXT NOT NULL,
    partition    INT NOT NULL,
    msg_offset   BIGINT NOT NULL,
    event_type   TEXT NOT NULL,
    aggregate_id UUID NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_inbox_events_received_at
    ON inbox_events (received_at);