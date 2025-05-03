-- Optimize lookups for player progress by user and story
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_player_progress_user_story ON player_progress (user_id, published_story_id);

-- Optimize lookups for player game states by player and story
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_player_game_states_player_story ON player_game_states (player_id, published_story_id);

-- Optimize lookups for stories liked by a user
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_story_likes_user ON story_likes (user_id); 