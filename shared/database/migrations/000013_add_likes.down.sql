-- +goose Down
-- Drop the story_likes table
DROP TABLE IF EXISTS story_likes;

-- Remove likes_count column from published_stories
ALTER TABLE published_stories
DROP COLUMN IF EXISTS likes_count; 