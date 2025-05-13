-- +migrate Down
ALTER TABLE story_scenes
  ALTER COLUMN scene_content TYPE TEXT USING scene_content::text; 