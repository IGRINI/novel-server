-- +migrate Up
ALTER TYPE story_status ADD VALUE IF NOT EXISTS 'first_scene_pending';
-- +migrate StatementEnd 