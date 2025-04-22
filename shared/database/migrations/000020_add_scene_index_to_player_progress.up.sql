-- Add scene_index column to player_progress table
-- Defaulting to 1 assuming existing progress is at least at the first scene
ALTER TABLE player_progress
ADD COLUMN scene_index INTEGER NOT NULL DEFAULT 1; 