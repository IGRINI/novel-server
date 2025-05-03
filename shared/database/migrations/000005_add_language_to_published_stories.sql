-- +migrate Up
ALTER TABLE published_stories
ADD COLUMN language VARCHAR(10) NOT NULL DEFAULT 'en';

COMMENT ON COLUMN published_stories.language IS 'Язык опубликованной истории (копируется из конфига при публикации).';

-- +migrate Down
ALTER TABLE published_stories
DROP COLUMN IF EXISTS language; 