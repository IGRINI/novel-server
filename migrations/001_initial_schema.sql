-- +migrate Up
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Таблица для хранения новелл
CREATE TABLE IF NOT EXISTS novels (
    novel_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id VARCHAR(255) NOT NULL,
    title VARCHAR(255) NOT NULL,
    config_data JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Индекс для быстрого поиска новелл пользователя
CREATE INDEX IF NOT EXISTS idx_novels_user_id ON novels(user_id);

-- Таблица для хранения состояний новелл
CREATE TABLE IF NOT EXISTS novel_states (
    novel_id UUID NOT NULL REFERENCES novels(novel_id) ON DELETE CASCADE,
    scene_index INTEGER NOT NULL,
    state_data JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (novel_id, scene_index)
);

-- Индекс для быстрого поиска последнего состояния
CREATE INDEX IF NOT EXISTS idx_novel_states_novel_id_scene ON novel_states(novel_id, scene_index DESC);

-- Триггер для автоматического обновления updated_at
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_novels_updated_at
    BEFORE UPDATE ON novels
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- +migrate Down
DROP TRIGGER IF EXISTS update_novels_updated_at ON novels;
DROP FUNCTION IF EXISTS update_updated_at_column();
DROP TABLE IF EXISTS novel_states;
DROP TABLE IF EXISTS novels;
DROP EXTENSION IF EXISTS "uuid-ossp"; 