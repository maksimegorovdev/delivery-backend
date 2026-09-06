DELETE FROM addresses
WHERE user_id IN (
    SELECT id
    FROM users
    WHERE email = 'demo@example.com'
);

DELETE FROM users
WHERE email = 'demo@example.com';
