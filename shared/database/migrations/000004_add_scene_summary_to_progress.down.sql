-- Remove the current_scene_summary column from player_progress
ALTER TABLE player_progress
DROP COLUMN IF EXISTS current_scene_summary; 