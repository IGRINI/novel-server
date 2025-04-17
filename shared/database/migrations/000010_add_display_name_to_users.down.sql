-- Удаляем комментарий (опционально, но хорошая практика)
COMMENT ON COLUMN users.display_name IS NULL;

-- Удаляем колонку display_name
ALTER TABLE users
DROP COLUMN display_name; 