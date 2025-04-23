-- +migrate Up

-- Update existing published_stories to map old statuses to the new 'initial_generation' introduced in the previous migration
UPDATE published_stories
SET status = 'initial_generation'::story_status
WHERE status IN ('generating_scene'::story_status, 'generating'::story_status);

-- Update other statuses to 'ready' as part of the same refactoring effort
UPDATE published_stories
SET status = 'ready'::story_status
WHERE status IN ('completed'::story_status, 'game_over_pending'::story_status); 