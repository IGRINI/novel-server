-- +migrate Down
-- Возвращаем уникальный индекс в player_progress (как в 000022)
CREATE UNIQUE INDEX IF NOT EXISTS idx_player_progress_user_story ON player_progress (user_id, published_story_id);

-- Возвращаем уникальный индекс/ограничение в player_game_states (как в 000022)
-- Используем CREATE INDEX, так как ALTER TABLE ADD CONSTRAINT IF NOT EXISTS менее стандартен
CREATE UNIQUE INDEX IF NOT EXISTS unique_player_story_game_state ON player_game_states (player_id, published_story_id); 