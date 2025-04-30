-- +migrate Up
ALTER TYPE story_status ADD VALUE IF NOT EXISTS 'generating';
-- +migrate StatementEnd 