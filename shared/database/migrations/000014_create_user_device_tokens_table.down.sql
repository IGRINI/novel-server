-- Удаление индексов и таблицы user_device_tokens
DROP INDEX IF EXISTS idx_user_device_tokens_token;
DROP INDEX IF EXISTS idx_user_device_tokens_user_id;
DROP TABLE IF EXISTS user_device_tokens; 