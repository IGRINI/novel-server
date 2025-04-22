-- +migrate Down
ALTER TABLE player_progress
DROP COLUMN last_story_summary,
DROP COLUMN last_future_direction,
DROP COLUMN last_var_impact_summary; 