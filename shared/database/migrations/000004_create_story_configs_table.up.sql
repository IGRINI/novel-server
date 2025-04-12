-- +migrate Up
-- Создание таблицы story_configs

-- Статус генерации истории
CREATE TYPE generation_status AS ENUM (
    'pending',     -- Ожидает обработки AI (только для начальной генерации)
    'generating',  -- В процессе генерации AI
    'draft',       -- Сгенерировано, готово к игре/ревизии
    'error'        -- Ошибка во время генерации
);

CREATE TABLE IF NOT EXISTS story_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(), -- UUID первичный ключ
    user_id BIGINT NOT NULL,                       -- ID пользователя, ссылка на users.id
    title TEXT,                                    -- Название истории (из JSON)
    description TEXT,                              -- Краткое описание истории (из JSON)
    status generation_status NOT NULL DEFAULT 'pending', -- Статус генерации
    user_input JSONB NOT NULL DEFAULT '[]'::jsonb, -- Массив пользовательских вводов (JSONB)
    config JSONB DEFAULT NULL,                     -- Актуальный JSON конфиг от Narrator (добавится позже)
    error_details TEXT DEFAULT NULL,               -- Детали последней ошибки генерации
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Ограничение внешнего ключа
    CONSTRAINT fk_user
        FOREIGN KEY(user_id)
        REFERENCES users(id)
        ON DELETE CASCADE -- Удалять конфиги при удалении пользователя
);

-- Индексы
CREATE INDEX IF NOT EXISTS idx_story_configs_user_id ON story_configs (user_id);
CREATE INDEX IF NOT EXISTS idx_story_configs_status ON story_configs (status);

-- Триггер для автоматического обновления updated_at
CREATE OR REPLACE FUNCTION trigger_set_timestamp()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER set_story_configs_timestamp
BEFORE UPDATE ON story_configs
FOR EACH ROW
EXECUTE FUNCTION trigger_set_timestamp();

-- Комментарии
COMMENT ON TABLE story_configs IS 'Хранит конфигурации и метаданные историй, создаваемых пользователями.';
COMMENT ON COLUMN story_configs.id IS 'Уникальный идентификатор конфигурации истории (UUID).';
COMMENT ON COLUMN story_configs.user_id IS 'Идентификатор пользователя, создавшего историю.';
COMMENT ON COLUMN story_configs.title IS 'Название истории, извлеченное из последнего сгенерированного JSON конфига.';
COMMENT ON COLUMN story_configs.description IS 'Краткое описание истории, извлеченное из последнего сгенерированного JSON конфига.';
COMMENT ON COLUMN story_configs.status IS 'Текущий статус процесса генерации истории (pending, generating, draft, error).';
COMMENT ON COLUMN story_configs.user_input IS 'История пользовательских запросов (промптов) в виде JSON массива строк.';
COMMENT ON COLUMN story_configs.config IS 'Полный JSON конфигурации истории, сгенерированный AI (Narrator). Добавляется миграцией 000005.';
COMMENT ON COLUMN story_configs.error_details IS 'Текст последней ошибки, возникшей при генерации.';
COMMENT ON COLUMN story_configs.created_at IS 'Время создания записи конфигурации.';
COMMENT ON COLUMN story_configs.updated_at IS 'Время последнего обновления записи конфигурации.';
COMMENT ON CONSTRAINT fk_user ON story_configs IS 'Внешний ключ, связывающий с таблицей users.';
COMMENT ON TYPE generation_status IS 'Перечисление возможных статусов генерации истории.';
