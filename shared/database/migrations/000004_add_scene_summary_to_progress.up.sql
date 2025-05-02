-- Add a nullable text column to store the summary of the scene associated with this progress state
ALTER TABLE player_progress
ADD COLUMN current_scene_summary TEXT NULL; 