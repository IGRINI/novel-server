-- Remove the internal_generation_step column from published_stories
ALTER TABLE published_stories
DROP COLUMN IF EXISTS internal_generation_step; 