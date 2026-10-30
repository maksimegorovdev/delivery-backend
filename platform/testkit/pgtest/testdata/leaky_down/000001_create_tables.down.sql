-- Намеренно неполный откат: audit_log остаётся, CheckMigrations должен это поймать.
DROP TABLE items;
