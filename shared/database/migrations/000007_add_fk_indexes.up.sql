-- Add indexes to foreign key columns often used in JOINs

CREATE INDEX IF NOT EXISTS idx_story_likes_published_story_id ON story_likes (published_story_id);

CREATE INDEX IF NOT EXISTS idx_player_progress_published_story_id ON player_progress (published_story_id); 