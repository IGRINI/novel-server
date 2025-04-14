-- +migrate Up
-- Файл: 000009_add_is_banned_to_users.up.sql

ALTER TABLE users
ADD COLUMN is_banned BOOLEAN NOT NULL DEFAULT FALSE;

-- Добавим индекс для быстрого поиска забаненных/незабаненных
CREATE INDEX IF NOT EXISTS idx_users_is_banned ON users (is_banned); 