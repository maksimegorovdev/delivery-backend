CREATE TABLE notifications (
    id              UUID PRIMARY KEY DEFAULT uuidv7(),
    event_id        UUID NOT NULL,
    event_type      TEXT NOT NULL,
    channel         TEXT NOT NULL,
    payload         JSONB NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_until    TIMESTAMPTZ,
    last_error      TEXT,
    sent_at         TIMESTAMPTZ,

    CONSTRAINT uq_notifications_event_channel UNIQUE (event_id, channel),
    CONSTRAINT chk_notifications_channel CHECK (channel IN ('log')),
    CONSTRAINT chk_notifications_status CHECK (status IN ('pending', 'sent', 'failed'))
);

CREATE INDEX idx_notifications_pending_next_attempt_at
    ON notifications (next_attempt_at)
    WHERE status = 'pending';