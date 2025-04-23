-- === Player Progress Refactoring ===

-- Add primary key to player_progress
ALTER TABLE player_progress ADD COLUMN id UUID PRIMARY KEY DEFAULT gen_random_uuid();

-- Make existing foreign key columns nullable
ALTER TABLE player_progress ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE player_progress ALTER COLUMN published_story_id DROP NOT NULL;

-- Add unique constraint for identifying progress nodes efficiently
-- This also creates an index for GetByStoryIDAndHash lookup
ALTER TABLE player_progress ADD CONSTRAINT unique_story_state_hash UNIQUE (published_story_id, current_state_hash);

-- Add back a unique index for (user_id, published_story_id) if needed
-- Ensures a user still has only one progress entry per story logically tied to their UserID in this table.
CREATE UNIQUE INDEX IF NOT EXISTS idx_player_progress_user_story ON player_progress (user_id, published_story_id);


-- === Player Game State Implementation ===

-- Create the new ENUM type for player game status
CREATE TYPE player_game_status AS ENUM (
    'playing',
    'generating_scene',
    'game_over_pending',
    'completed',
    'error'
);

-- Create the player_game_states table
CREATE TABLE player_game_states (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id UUID NOT NULL, -- Assuming a 'users' table exists with UUID primary key
    published_story_id UUID NOT NULL REFERENCES published_stories(id) ON DELETE CASCADE,
    player_progress_id UUID NULL, -- Link to the specific progress node
    current_scene_id UUID NULL REFERENCES story_scenes(id) ON DELETE SET NULL, -- Link to the scene displayed for this state
    player_status player_game_status NOT NULL,
    ending_text TEXT NULL,
    error_details TEXT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_activity_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ NULL,

    -- Unique constraint to ensure one active game state per player per story
    CONSTRAINT unique_player_story_game_state UNIQUE (player_id, published_story_id),

    -- Add foreign key constraint for player_progress_id (referencing the now existing player_progress.id)
    CONSTRAINT fk_player_progress
    FOREIGN KEY (player_progress_id) REFERENCES player_progress(id) ON DELETE SET NULL
);

-- Add indexes for common lookups
CREATE INDEX idx_player_game_states_player_id ON player_game_states(player_id);
CREATE INDEX idx_player_game_states_published_story_id ON player_game_states(published_story_id);
CREATE INDEX idx_player_game_states_last_activity ON player_game_states(last_activity_at);
CREATE INDEX idx_player_game_states_player_progress_id ON player_game_states(player_progress_id);


-- === Story Status Update ===

-- Add the new 'initial_generation' status to the existing story_status ENUM
ALTER TYPE story_status ADD VALUE IF NOT EXISTS 'initial_generation';

-- Update existing published_stories to map old statuses will be moved to the next migration 