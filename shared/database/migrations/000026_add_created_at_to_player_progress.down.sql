-- +migrate Down
-- Удаляем колонку created_at из таблицы player_progress

ALTER TABLE player_progress
DROP COLUMN IF EXISTS created_at; 