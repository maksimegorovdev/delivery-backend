INSERT INTO products (name, price)
VALUES
    ('Минеральная вода Borjomi', 15900),
    ('Coca-Cola', 17900)
ON CONFLICT (name)
DO NOTHING;