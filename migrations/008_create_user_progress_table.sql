-- +migrate Up

-- Создаем таблицу для отслеживания прогресса пользователя в новелле
CREATE TABLE IF NOT EXISTS user_novel_progress (
    novel_id UUID NOT NULL REFERENCES novels(novel_id) ON DELETE CASCADE,
    user_id VARCHAR(255) NOT NULL,
    current_scene_index INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (novel_id, user_id)
);

-- Индекс для быстрого поиска прогресса по пользователю
CREATE INDEX IF NOT EXISTS idx_user_novel_progress_user_id ON user_novel_progress(user_id);

-- Заполняем таблицу данными из существующих состояний
INSERT INTO user_novel_progress (novel_id, user_id, current_scene_index, created_at, updated_at)
SELECT 
    novel_id, 
    user_id, 
    MAX(scene_index) as current_scene_index,
    MIN(created_at) as created_at,
    MAX(updated_at) as updated_at
FROM novel_states
GROUP BY novel_id, user_id
ON CONFLICT (novel_id, user_id) DO UPDATE
SET current_scene_index = EXCLUDED.current_scene_index,
    updated_at = EXCLUDED.updated_at;

-- Триггер для автоматического обновления updated_at
CREATE TRIGGER update_user_novel_progress_updated_at
    BEFORE UPDATE ON user_novel_progress
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- +migrate Down

-- Удаляем триггер
DROP TRIGGER IF EXISTS update_user_novel_progress_updated_at ON user_novel_progress;

-- Удаляем индекс
DROP INDEX IF EXISTS idx_user_novel_progress_user_id;

-- Удаляем таблицу
DROP TABLE IF EXISTS user_novel_progress; 