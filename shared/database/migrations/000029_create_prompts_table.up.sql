-- Миграция для создания таблицы prompts

-- Функция для автоматического обновления поля updated_at
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
   NEW.updated_at = NOW();
   RETURN NEW;
END;
$$ language 'plpgsql';

-- Создание таблицы prompts
CREATE TABLE prompts (
    id SERIAL PRIMARY KEY,
    key VARCHAR(255) NOT NULL,
    language VARCHAR(10) NOT NULL,  -- Достаточно места для кодов типа en-US, если понадобится
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,

    -- Уникальный ключ для пары (key, language)
    UNIQUE (key, language)
);

-- Создание триггера для обновления updated_at
CREATE TRIGGER update_prompts_updated_at
BEFORE UPDATE ON prompts
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

-- Добавляем индексы для ускорения поиска
CREATE INDEX idx_prompts_key_language ON prompts (key, language); 