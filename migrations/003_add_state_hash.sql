-- +migrate Up
-- Добавляем колонку state_hash в таблицу novel_states
ALTER TABLE novel_states ADD COLUMN state_hash VARCHAR(255);

-- Создаем индекс для быстрого поиска по хешу
CREATE INDEX idx_novel_states_state_hash ON novel_states(state_hash);

-- +migrate Down
-- Удаляем индекс
DROP INDEX IF EXISTS idx_novel_states_state_hash;

-- Удаляем колонку state_hash
ALTER TABLE novel_states DROP COLUMN state_hash; 