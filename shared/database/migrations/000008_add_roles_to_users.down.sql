-- +migrate Down
-- Remove roles column from users table
ALTER TABLE users
DROP COLUMN roles;

-- Drop the index if you created it in the up migration
-- DROP INDEX IF EXISTS idx_users_roles;
