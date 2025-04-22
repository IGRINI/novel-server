-- +migrate Down
ALTER TABLE player_progress DROP CONSTRAINT IF EXISTS player_progress_pkey; 