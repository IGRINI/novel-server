-- Add the new internal_generation_step column to published_stories
ALTER TABLE published_stories
ADD COLUMN internal_generation_step VARCHAR(50) NULL;

COMMENT ON COLUMN published_stories.internal_generation_step IS 'Internal state indicating the current or last executed step of the initial story generation process. Used for more precise retries.';

-- Optionally, initialize existing stories based on their current status
-- This is just an example, adjust logic based on actual needs
UPDATE published_stories
SET internal_generation_step = 'complete'
WHERE status = 'ready';

-- Set a default step for stories that were in error or pending states
-- (adjust this logic as needed for existing data)
UPDATE published_stories
SET internal_generation_step = 'setup_generation'
WHERE status IN ('error', 'setup_pending') AND internal_generation_step IS NULL;

-- Add more specific updates based on other statuses if possible
-- UPDATE published_stories SET internal_generation_step = 'protagonist_goal' WHERE status = 'protagonist_goal_pending' AND internal_generation_step IS NULL;
-- UPDATE published_stories SET internal_generation_step = 'scene_planner' WHERE status = 'scene_planner_pending' AND internal_generation_step IS NULL;
-- etc. 