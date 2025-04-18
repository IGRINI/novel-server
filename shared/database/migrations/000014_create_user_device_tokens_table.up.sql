-- Таблица для хранения токенов устройств пользователей для push-уведомлений
CREATE TABLE IF NOT EXISTS user_device_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- Ссылка на пользователя
    token TEXT NOT NULL,             -- Токен устройства (FCM или APNS)
    platform VARCHAR(10) NOT NULL,     -- Платформа ('android' или 'ios')
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), -- Время последнего использования/обновления

    -- Уникальный ключ для комбинации user_id и token, чтобы избежать дубликатов
    CONSTRAINT uq_user_device_token UNIQUE (user_id, token)
);

-- Индекс для быстрого поиска токенов по user_id
CREATE INDEX IF NOT EXISTS idx_user_device_tokens_user_id ON user_device_tokens(user_id);
-- Индекс для возможного поиска по токену (например, для удаления невалидных)
CREATE INDEX IF NOT EXISTS idx_user_device_tokens_token ON user_device_tokens(token); 