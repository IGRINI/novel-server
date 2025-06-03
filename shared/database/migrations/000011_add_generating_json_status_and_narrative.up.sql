BEGIN;

-- Add new value to the player_game_status ENUM type
ALTER TYPE player_game_status ADD VALUE IF NOT EXISTS 'generating_json';

-- Add the new column to the player_game_states table
ALTER TABLE player_game_states
ADD COLUMN IF NOT EXISTS pending_narrative TEXT;

COMMIT; 