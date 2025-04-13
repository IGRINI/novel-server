-- +migrate Up
CREATE TABLE player_progress (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- Или UUID
    published_story_id UUID NOT NULL REFERENCES published_stories(id) ON DELETE CASCADE,
    current_core_stats JSONB NOT NULL,
    current_story_variables JSONB NOT NULL DEFAULT '{}'::jsonb,
    current_global_flags TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    current_state_hash VARCHAR(64) NOT NULL, -- SHA256 в hex
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, published_story_id) -- Композитный первичный ключ
);

-- Индекс для быстрого поиска прогресса по хэшу (хотя поиск скорее будет по user_id + story_id)
CREATE INDEX idx_player_progress_state_hash ON player_progress(current_state_hash);

-- Триггер для обновления updated_at (используем ту же функцию, что и для published_stories)
CREATE TRIGGER update_player_progress_updated_at
BEFORE UPDATE ON player_progress
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column(); 