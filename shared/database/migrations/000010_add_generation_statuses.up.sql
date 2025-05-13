BEGIN;

-- Add new values to the story_status ENUM type
-- Use ALTER TYPE ... ADD VALUE IF NOT EXISTS to prevent errors if run multiple times
ALTER TYPE story_status ADD VALUE IF NOT EXISTS 'image_generation_pending';
ALTER TYPE story_status ADD VALUE IF NOT EXISTS 'json_generation_pending';

COMMIT; 