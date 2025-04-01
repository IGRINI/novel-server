-- +migrate Up

-- Создаем таблицу для хранения динамических элементов прогресса пользователя в истории
CREATE TABLE IF NOT EXISTS user_story_progress (
    novel_id UUID NOT NULL REFERENCES novels(novel_id) ON DELETE CASCADE,
    user_id VARCHAR(255) NOT NULL,
    scene_index INTEGER NOT NULL DEFAULT 0,
    global_flags JSONB NOT NULL DEFAULT '[]'::JSONB,
    relationship JSONB NOT NULL DEFAULT '{}'::JSONB,
    story_variables JSONB NOT NULL DEFAULT '{}'::JSONB,
    previous_choices JSONB NOT NULL DEFAULT '[]'::JSONB,
    story_summary_so_far TEXT NOT NULL DEFAULT '',
    future_direction TEXT NOT NULL DEFAULT '',
    state_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (novel_id, user_id, scene_index)
);

-- Индекс для быстрого поиска по хешу состояния
CREATE INDEX idx_user_story_progress_state_hash ON user_story_progress(state_hash);

-- Индекс для быстрого поиска прогресса по пользователю
CREATE INDEX idx_user_story_progress_user_id ON user_story_progress(user_id);

-- Индекс для быстрого поиска последней сцены пользователя
CREATE INDEX idx_user_story_progress_latest ON user_story_progress(novel_id, user_id, scene_index DESC);

-- Заполняем таблицу данными из существующих состояний
INSERT INTO user_story_progress 
    (novel_id, user_id, scene_index, global_flags, relationship, story_variables, 
     previous_choices, story_summary_so_far, future_direction, state_hash, created_at, updated_at)
SELECT 
    ns.novel_id, 
    ns.user_id, 
    ns.scene_index,
    COALESCE(state_data::jsonb->'global_flags', '[]'::jsonb) as global_flags,
    COALESCE(state_data::jsonb->'relationship', '{}'::jsonb) as relationship,
    COALESCE(state_data::jsonb->'story_variables', '{}'::jsonb) as story_variables,
    COALESCE(state_data::jsonb->'previous_choices', '[]'::jsonb) as previous_choices,
    COALESCE(state_data::jsonb->>'story_summary_so_far', '') as story_summary_so_far,
    COALESCE(state_data::jsonb->>'future_direction', '') as future_direction,
    ns.state_hash,
    ns.created_at,
    ns.updated_at
FROM novel_states ns
ON CONFLICT (novel_id, user_id, scene_index) DO UPDATE
SET global_flags = EXCLUDED.global_flags,
    relationship = EXCLUDED.relationship,
    story_variables = EXCLUDED.story_variables,
    previous_choices = EXCLUDED.previous_choices,
    story_summary_so_far = EXCLUDED.story_summary_so_far,
    future_direction = EXCLUDED.future_direction,
    state_hash = EXCLUDED.state_hash,
    updated_at = EXCLUDED.updated_at;

-- Триггер для автоматического обновления updated_at
CREATE TRIGGER update_user_story_progress_updated_at
    BEFORE UPDATE ON user_story_progress
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- +migrate Down

-- Удаляем триггер
DROP TRIGGER IF EXISTS update_user_story_progress_updated_at ON user_story_progress;

-- Удаляем индексы
DROP INDEX IF EXISTS idx_user_story_progress_state_hash;
DROP INDEX IF EXISTS idx_user_story_progress_user_id;
DROP INDEX IF EXISTS idx_user_story_progress_latest;

-- Удаляем таблицу
DROP TABLE IF EXISTS user_story_progress; 