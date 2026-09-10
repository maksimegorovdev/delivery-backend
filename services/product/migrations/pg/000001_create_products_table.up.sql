CREATE TABLE products (
    id         UUID PRIMARY KEY DEFAULT uuidv7(),
    name       TEXT NOT NULL UNIQUE,
    price      BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_products_price CHECK (price >= 0)
);