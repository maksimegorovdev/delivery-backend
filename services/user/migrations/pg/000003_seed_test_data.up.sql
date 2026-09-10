WITH ins_user AS (
    INSERT INTO users (email, first_name, last_name)
    VALUES
        ('demo@example.com', 'Demo', 'User')
    ON CONFLICT (email)
    DO NOTHING
    RETURNING id
),
demo_user AS (
    SELECT id FROM ins_user
    UNION ALL
    SELECT id
    FROM users
    WHERE email = 'demo@example.com'
    LIMIT 1
);

INSERT INTO addresses (user_id, address)
SELECT
    du.id,
    'г. Москва, ул. Пушкина, д. 1, кв. 1'
FROM demo_user du
WHERE NOT EXISTS (
    SELECT 1
    FROM addresses a
    WHERE a.user_id = du.id
      AND a.address = 'г. Москва, ул. Пушкина, д. 1, кв. 1'
);