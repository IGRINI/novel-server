-- +migrate Down
DROP TRIGGER IF EXISTS update_published_stories_updated_at ON published_stories;
DROP FUNCTION IF EXISTS update_updated_at_column(); -- Удаляем, если она больше нигде не нужна
DROP TABLE IF EXISTS published_stories;
DROP TYPE IF EXISTS story_status; 