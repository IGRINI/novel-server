-- +migrate Up

-- Добавляем колонку для хранения сетапа новеллы (сцена 0) в таблице novels
ALTER TABLE novels ADD COLUMN IF NOT EXISTS setup_state_data JSONB;

-- Для существующих новелл: перенесем сетап из таблицы novel_states
-- Для каждой новеллы берем самое раннее состояние с scene_index = 0
UPDATE novels n
SET setup_state_data = (
    SELECT state_data
    FROM novel_states ns
    WHERE ns.novel_id = n.novel_id
    AND ns.scene_index = 0
    ORDER BY ns.created_at ASC
    LIMIT 1
)
WHERE EXISTS (
    SELECT 1
    FROM novel_states ns
    WHERE ns.novel_id = n.novel_id
    AND ns.scene_index = 0
);

-- +migrate Down

-- Удаляем колонку с сетапом
ALTER TABLE novels DROP COLUMN IF EXISTS setup_state_data; 