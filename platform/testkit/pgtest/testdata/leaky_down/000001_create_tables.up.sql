-- IF NOT EXISTS: повторный up проходит и при неполном down, поэтому ловить его должна только проверка схемы.
CREATE TABLE IF NOT EXISTS items (
    id   BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_log (
    id      BIGSERIAL PRIMARY KEY,
    message TEXT NOT NULL
);
