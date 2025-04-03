-- Создаем таблицу для кеширования сцен по хешу состояния
CREATE TABLE IF NOT EXISTS scene_batches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    novel_id UUID NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    state_hash TEXT NOT NULL, -- Хеш состояния, которое привело к этой сцене
    story_summary_so_far TEXT NOT NULL, -- Текущее состояние истории
    future_direction TEXT NOT NULL, -- Направление развития
    choices JSONB NOT NULL, -- Массив событий с выборами
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(novel_id, state_hash) -- Уникальный индекс для поиска по хешу
); 