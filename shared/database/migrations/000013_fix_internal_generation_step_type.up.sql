-- Create the ENUM type for internal generation steps if it doesn't exist
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'internal_generation_step') THEN
        CREATE TYPE internal_generation_step AS ENUM (
            'moderation',
            'protagonist_goal',
            'scene_planner',
            'character_generation',
            'card_image_generation',
            'setup_generation',
            'cover_image_generation',
            'character_image_generation',
            'initial_scene_json',
            'complete'
        );
--        RAISE NOTICE 'ENUM type internal_generation_step created.';
--    ELSE
--        RAISE NOTICE 'ENUM type internal_generation_step already exists.';
    END IF;
END
$$;

-- Check if the column exists and is VARCHAR, if so, drop it before recreating
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' -- Adjust schema if needed
          AND table_name = 'published_stories'
          AND column_name = 'internal_generation_step'
          AND data_type = 'character varying'
    ) THEN
--        RAISE NOTICE 'Dropping existing VARCHAR column internal_generation_step.';
        ALTER TABLE published_stories DROP COLUMN internal_generation_step;
    END IF;
END
$$;

-- Add the internal_generation_step column with the correct ENUM type if it doesn't exist
ALTER TABLE published_stories
ADD COLUMN IF NOT EXISTS internal_generation_step internal_generation_step NULL;

COMMENT ON COLUMN published_stories.internal_generation_step IS 'Internal state indicating the current or last executed step of the initial story generation process. Used for more precise retries.';

-- Initialize existing stories based on their current status, casting to the ENUM type
-- Only update rows where internal_generation_step IS NULL to avoid overwriting potentially correct values
-- if this migration is run multiple times or after manual fixes.
UPDATE published_stories
SET internal_generation_step = 'complete'::internal_generation_step
WHERE status = 'ready' AND internal_generation_step IS NULL;

UPDATE published_stories
SET internal_generation_step = 'setup_generation'::internal_generation_step
WHERE status IN ('error', 'setup_pending') AND internal_generation_step IS NULL;

UPDATE published_stories
SET internal_generation_step = 'initial_scene_json'::internal_generation_step -- Corrected: Corresponds to generating the first scene JSON
WHERE status = 'first_scene_pending' AND internal_generation_step IS NULL;

UPDATE published_stories
SET internal_generation_step = 'protagonist_goal'::internal_generation_step
WHERE status = 'protagonist_goal_pending' AND internal_generation_step IS NULL;

UPDATE published_stories
SET internal_generation_step = 'scene_planner'::internal_generation_step
WHERE status = 'scene_planner_pending' AND internal_generation_step IS NULL;

-- Add more specific updates based on other statuses if needed.
-- Consider the logic for 'sub_tasks_pending', 'image_generation_pending', 'json_generation_pending' etc.
-- For example, if sub_tasks_pending usually follows scene_planner:
-- UPDATE published_stories SET internal_generation_step = 'character_generation'::internal_generation_step WHERE status = 'sub_tasks_pending' AND internal_generation_step IS NULL;


-- RAISE NOTICE 'Migration 000013 UP applied: internal_generation_step column corrected and initialized.'; 