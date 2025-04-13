-- +migrate Down
DROP TRIGGER IF EXISTS update_player_progress_updated_at ON player_progress;
DROP TABLE IF EXISTS player_progress;
-- Функцию update_updated_at_column НЕ удаляем здесь, она может использоваться published_stories 