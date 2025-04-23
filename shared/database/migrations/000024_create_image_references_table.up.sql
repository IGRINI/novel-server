-- Создаем таблицу для хранения ссылок на изображения и их URL
CREATE TABLE IF NOT EXISTS image_references (
    reference TEXT PRIMARY KEY,
    image_url TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Создаем или заменяем функцию для обновления updated_at
-- (Используем CREATE OR REPLACE для идемпотентности, хотя в миграциях это менее критично)
CREATE OR REPLACE FUNCTION trigger_set_timestamp()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Создаем триггер для вызова функции перед обновлением строки
-- (Добавляем IF NOT EXISTS для большей устойчивости при повторном применении, хотя migrate обычно этого не делает)
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'set_timestamp_image_references') THEN
    CREATE TRIGGER set_timestamp_image_references
    BEFORE UPDATE ON image_references
    FOR EACH ROW
    EXECUTE PROCEDURE trigger_set_timestamp();
  END IF;
END
$$;

-- Добавляем комментарии к таблице и колонкам для ясности
COMMENT ON TABLE image_references IS 'Stores generated image URLs keyed by a deterministic reference string.';
COMMENT ON COLUMN image_references.reference IS 'Deterministic reference string (e.g., ch_male_adult_fantasy_...)';
COMMENT ON COLUMN image_references.image_url IS 'URL of the generated and stored image.';
COMMENT ON COLUMN image_references.created_at IS 'Timestamp when the reference was first created.';
COMMENT ON COLUMN image_references.updated_at IS 'Timestamp when the reference or URL was last updated.'; 