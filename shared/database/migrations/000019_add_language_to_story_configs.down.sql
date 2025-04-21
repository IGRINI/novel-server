-- +goose Down
-- Remove language column from story_configs table
ALTER TABLE story_configs
DROP COLUMN language; 