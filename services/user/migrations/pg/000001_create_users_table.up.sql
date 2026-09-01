CREATE TABLE IF NOT EXISTS users (
    id         UUID PRIMARY KEY DEFAULT uuidv7(),
    email      TEXT NOT NULL,
    first_name TEXT NOT NULL,
    last_name  TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ DEFAULT NULL,

    CONSTRAINT chk_users_email CHECK (email = LOWER(email) AND LENGTH(email) <= 255),
    CONSTRAINT chk_users_first_name CHECK (LENGTH(first_name) <= 255),
    CONSTRAINT chk_users_last_name  CHECK (LENGTH(last_name)  <= 255)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_users_email_not_deleted
    ON users (email)
    WHERE deleted_at IS NULL;