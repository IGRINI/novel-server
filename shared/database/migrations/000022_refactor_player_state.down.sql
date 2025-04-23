-- Revert status changes in published_stories (best effort)
UPDATE published_stories
SET status = 'generating'::story_status
WHERE status = 'initial_generation'::story_status;
-- Skipping complex revert for 'ready' mapping

-- Drop player_game_states table (this implicitly drops indexes and FK constraint to player_progress)
DROP TABLE IF EXISTS player_game_states;

-- Drop the player_game_status ENUM type
DROP TYPE IF EXISTS player_game_status;

-- Revert player_progress table changes
ALTER TABLE player_progress DROP CONSTRAINT IF EXISTS unique_story_state_hash;
DROP INDEX IF EXISTS idx_player_progress_user_story; -- Remove index added in up migration

-- Attempt to make columns NOT NULL again (potentially unsafe, commented out)
-- ALTER TABLE player_progress ALTER COLUMN user_id SET NOT NULL;
-- ALTER TABLE player_progress ALTER COLUMN published_story_id SET NOT NULL;

ALTER TABLE player_progress DROP COLUMN IF EXISTS id;

-- Re-add old composite primary key
ALTER TABLE player_progress ADD PRIMARY KEY (user_id, published_story_id);

-- We don't remove the 'initial_generation' value from story_status 