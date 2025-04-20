-- +migrate Up
-- Добавляем колонку updated_at
ALTER TABLE story_scenes ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- Добавляем триггер для автоматического обновления updated_at
-- Используем ту же функцию, что и для published_stories
CREATE TRIGGER update_story_scenes_updated_at
BEFORE UPDATE ON story_scenes
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column(); 