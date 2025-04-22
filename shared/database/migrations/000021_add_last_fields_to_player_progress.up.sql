-- +migrate Up
ALTER TABLE player_progress
ADD COLUMN last_story_summary TEXT NULL,
ADD COLUMN last_future_direction TEXT NULL,
ADD COLUMN last_var_impact_summary TEXT NULL;

COMMENT ON COLUMN player_progress.last_story_summary IS 'Последнее краткое изложение сюжета для этого прогресса';
COMMENT ON COLUMN player_progress.last_future_direction IS 'Последнее направление развития сюжета';
COMMENT ON COLUMN player_progress.last_var_impact_summary IS 'Последнее описание влияния переменных'; 