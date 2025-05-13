BEGIN;

-- Remove the column from the player_game_states table
ALTER TABLE player_game_states
DROP COLUMN IF EXISTS pending_narrative;

-- Removing ENUM values is complex and often discouraged.
-- We leave the 'generating_json' value in the player_status type.
-- If needed, it can be marked as deprecated in application logic.

COMMIT; 