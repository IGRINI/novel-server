-- +goose Up
-- Add language column to story_configs table
ALTER TABLE story_configs
ADD COLUMN language VARCHAR(10) NOT NULL DEFAULT 'en';

-- Optional: Update existing rows if needed, though default handles it
-- UPDATE story_configs SET language = 'en' WHERE language IS NULL; 