-- Remove indexes added in the corresponding UP migration

DROP INDEX IF EXISTS idx_story_likes_published_story_id;

DROP INDEX IF EXISTS idx_player_progress_published_story_id; 