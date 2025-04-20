-- +migrate Down
-- Удаляем триггер
DROP TRIGGER IF EXISTS update_story_scenes_updated_at ON story_scenes;

-- Удаляем колонку updated_at
ALTER TABLE story_scenes DROP COLUMN IF EXISTS updated_at; 