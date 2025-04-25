-- +migrate Up
-- Добавляем колонку created_at в таблицу player_progress

ALTER TABLE player_progress
ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- Обновляем существующие записи, устанавливая created_at равным updated_at
-- Это предположение, что время последнего обновления близко ко времени создания
-- для старых записей, где created_at не было.
UPDATE player_progress SET created_at = updated_at WHERE created_at IS NULL; 