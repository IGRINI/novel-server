-- +migrate Down
-- Файл: 000009_add_is_banned_to_users.down.sql

DROP INDEX IF EXISTS idx_users_is_banned;

ALTER TABLE users
DROP COLUMN IF EXISTS is_banned; 