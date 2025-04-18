-- +goose Up
-- Add likes_count column to published_stories
ALTER TABLE published_stories
ADD COLUMN likes_count BIGINT NOT NULL DEFAULT 0;

-- Create story_likes table
CREATE TABLE story_likes (
    user_id UUID NOT NULL,
    published_story_id UUID NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
    PRIMARY KEY (user_id, published_story_id),
    FOREIGN KEY (published_story_id) REFERENCES published_stories(id) ON DELETE CASCADE
);

-- Add index for faster counting per story
CREATE INDEX idx_story_likes_published_story_id ON story_likes(published_story_id); 