-- +migrate Up
CREATE TABLE story_scenes (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    published_story_id UUID NOT NULL REFERENCES published_stories(id) ON DELETE CASCADE,
    state_hash VARCHAR(64) NOT NULL, -- SHA256 в hex - 64 символа
    scene_content TEXT NOT NULL, -- Текст сцены от AI
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Индексы
CREATE INDEX idx_story_scenes_published_story_id ON story_scenes(published_story_id);
CREATE INDEX idx_story_scenes_state_hash ON story_scenes(state_hash);
-- Комбинированный индекс для поиска конкретной сцены по истории и хэшу
CREATE UNIQUE INDEX idx_story_scenes_story_hash ON story_scenes(published_story_id, state_hash); 