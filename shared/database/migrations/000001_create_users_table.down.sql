-- +migrate Down
-- SQL in this section is executed when the migration is rolled back.

DROP INDEX IF EXISTS idx_users_username;
DROP TABLE IF EXISTS users;

-- +migrate StatementEnd 