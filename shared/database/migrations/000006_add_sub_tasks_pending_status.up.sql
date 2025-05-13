-- Add the new status before any specific row uses it to avoid issues with existing constraints/checks.
-- The exact syntax might vary slightly based on your PostgreSQL version and how the type is used.
-- This is a common way to add a value to an existing ENUM type.

-- Disable and re-enable triggers if necessary, or ensure this is run in a transaction
-- that doesn't conflict with live data modifications if your application is running.

-- Verify (optional, for manual checking in psql):
-- SELECT unnest(enum_range(NULL::story_status)); 

-- Temporarily rename the old enum type
ALTER TYPE story_status RENAME TO story_status_old_for_sub_tasks;

-- Create the new enum type with all values (old and new, including sub_tasks_pending)
CREATE TYPE story_status AS ENUM (
    'draft',
    'moderation_pending',
    'protagonist_goal_pending',
    'scene_planner_pending',
    'sub_tasks_pending',
    'setup_pending',
    'image_generation_pending',
    'first_scene_pending',
    'initial_generation',
    'generating',
    'ready',
    'error'
);

-- Update existing tables to use the new enum type, casting from the old type
ALTER TABLE published_stories ALTER COLUMN status DROP DEFAULT;
ALTER TABLE published_stories ALTER COLUMN status TYPE story_status USING status::text::story_status;
ALTER TABLE published_stories ALTER COLUMN status SET DEFAULT 'setup_pending'::story_status;
-- Если есть другие таблицы, использующие story_status, их также нужно обновить здесь.
-- Например: ALTER TABLE player_game_states ALTER COLUMN status TYPE story_status USING status::text::story_status;

-- Drop the old enum type
DROP TYPE story_status_old_for_sub_tasks; 