-- +migrate Up
ALTER TABLE story_scenes
  ALTER COLUMN scene_content TYPE JSONB USING to_jsonb(scene_content); 