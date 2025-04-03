-- Удаляем внешний ключ, ссылающийся на старую таблицу scenes
ALTER TABLE novel_states DROP CONSTRAINT IF EXISTS novel_states_current_scene_id_fkey;

-- Удаляем старые таблицы (так как структура сильно меняется)
DROP TABLE IF EXISTS choices CASCADE;
DROP TABLE IF EXISTS scenes CASCADE;

-- Создаем новую структуру для сцен (батчей)
CREATE TABLE scenes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    novel_id UUID NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    batch_number INTEGER NOT NULL, -- Порядковый номер батча в новелле
    story_summary_so_far TEXT NOT NULL, -- Для ИИ: текущее состояние истории
    future_direction TEXT NOT NULL, -- Для ИИ: направление развития
    choices JSONB NOT NULL, -- Массив событий с выборами
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(novel_id, batch_number) -- Уникальный батч для новеллы
); 