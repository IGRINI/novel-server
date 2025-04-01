-- +migrate Up
-- Создаем таблицу для хранения черновиков новелл
CREATE TABLE IF NOT EXISTS novel_drafts (
    draft_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(), -- Используем uuid_generate_v4() если он доступен из 001
    user_id VARCHAR(255) NOT NULL,
    config_json JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Индекс для быстрого поиска черновиков пользователя
CREATE INDEX IF NOT EXISTS idx_novel_drafts_user_id ON novel_drafts(user_id);

-- Триггер для автоматического обновления updated_at для novel_drafts
-- Используем существующую функцию update_updated_at_column из 001_initial_schema.sql
CREATE TRIGGER update_novel_drafts_updated_at
    BEFORE UPDATE ON novel_drafts
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- +migrate Down
-- Удаляем триггер
DROP TRIGGER IF EXISTS update_novel_drafts_updated_at ON novel_drafts;

-- Удаляем индекс
DROP INDEX IF EXISTS idx_novel_drafts_user_id;

-- Удаляем таблицу
DROP TABLE IF EXISTS novel_drafts; 