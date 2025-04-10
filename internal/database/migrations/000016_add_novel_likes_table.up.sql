-- Добавляем колонку like_count в таблицу novels
ALTER TABLE novels ADD COLUMN like_count INTEGER NOT NULL DEFAULT 0;

-- Создаем таблицу novel_likes для хранения лайков
CREATE TABLE IF NOT EXISTS novel_likes (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    novel_id UUID NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, novel_id)
);

-- Создаем индексы для ускорения запросов
CREATE INDEX IF NOT EXISTS idx_novel_likes_user_id ON novel_likes(user_id);
CREATE INDEX IF NOT EXISTS idx_novel_likes_novel_id ON novel_likes(novel_id); 