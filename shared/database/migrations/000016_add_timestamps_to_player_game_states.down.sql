ALTER TABLE player_game_states
DROP COLUMN IF EXISTS updated_at,
DROP COLUMN IF EXISTS created_at; 