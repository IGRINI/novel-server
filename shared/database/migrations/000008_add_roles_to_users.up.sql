-- +migrate Up
-- Add roles column to users table
ALTER TABLE users
ADD COLUMN roles TEXT[] NOT NULL DEFAULT '{ROLE_USER}';

-- Optionally, add an index if you plan to query roles frequently
-- CREATE INDEX IF NOT EXISTS idx_users_roles ON users USING GIN (roles);
