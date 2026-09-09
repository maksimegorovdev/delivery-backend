CREATE TABLE orders (
    id               UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id          UUID NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending',
    total_amount     BIGINT NOT NULL,
    delivery_address TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_orders_status CHECK (status IN ('pending', 'confirmed', 'cancelled')),
    CONSTRAINT chk_orders_total_amount CHECK (total_amount >= 0)
);

CREATE INDEX idx_orders_user_id
    ON orders (user_id);

CREATE TABLE order_items (
    id           UUID PRIMARY KEY DEFAULT uuidv7(),
    order_id     UUID NOT NULL,
    product_id   UUID NOT NULL,
    product_name TEXT NOT NULL,
    quantity     INT NOT NULL,
    unit_price   BIGINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_order_items_order_id FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE,
    CONSTRAINT chk_order_items_quantity CHECK (quantity > 0),
    CONSTRAINT chk_order_items_unit_price CHECK (unit_price >= 0)
);

CREATE INDEX idx_order_items_order_id
    ON order_items (order_id);