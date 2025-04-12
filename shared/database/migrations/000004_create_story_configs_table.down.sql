-- +migrate Down
-- Удаление таблицы story_configs и связанных объектов

DROP TRIGGER IF EXISTS set_story_configs_timestamp ON story_configs;
DROP FUNCTION IF EXISTS trigger_set_timestamp(); -- Удаляем функцию, если она больше нигде не нужна
DROP INDEX IF EXISTS idx_story_configs_status;
DROP INDEX IF EXISTS idx_story_configs_user_id;
DROP TABLE IF EXISTS story_configs;
DROP TYPE IF EXISTS generation_status;
