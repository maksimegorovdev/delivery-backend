WITH ins_user AS (
    INSERT INTO users (id, email, first_name, last_name)
    VALUES
        ('01a0d1fa-a4e8-75a1-802d-d6e3c642b0f3', 'demo@example.com', 'Demo', 'User')
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
)

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