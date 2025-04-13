-- +migrate Up
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TYPE story_status AS ENUM (
    'setup_pending',
    'first_scene_pending',
    'ready',
    'error'
);

CREATE TABLE published_stories (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- Или UUID, если users.id - UUID
    config JSONB NOT NULL,
    setup JSONB,
    status story_status NOT NULL DEFAULT 'setup_pending',
    is_public BOOLEAN NOT NULL DEFAULT FALSE,
    is_adult_content BOOLEAN NOT NULL,
    title VARCHAR(255), -- Длина опциональна, можно убрать
    description TEXT,
    error_details TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Индексы для частых запросов
CREATE INDEX idx_published_stories_user_id ON published_stories(user_id);
CREATE INDEX idx_published_stories_status ON published_stories(status);
CREATE INDEX idx_published_stories_is_public ON published_stories(is_public); -- Для выборки публичных

-- Триггер для обновления updated_at
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
   NEW.updated_at = NOW();
   RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_published_stories_updated_at
BEFORE UPDATE ON published_stories
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column(); 