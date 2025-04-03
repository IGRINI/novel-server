-- Удаляем новую таблицу (батчей)
DROP TABLE IF EXISTS scenes;

-- Восстанавливаем старую структуру
CREATE TABLE scenes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    novel_id UUID NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    content TEXT NOT NULL,
    "order" INTEGER NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE choices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scene_id UUID NOT NULL REFERENCES scenes(id) ON DELETE CASCADE,
    text TEXT NOT NULL,
    next_scene_id UUID REFERENCES scenes(id),
    requirements JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Восстанавливаем внешний ключ в novel_states (если таблица существует)
-- Используем IF EXISTS для большей надежности
DO $$
BEGIN
   IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'novel_states') THEN
      ALTER TABLE novel_states ADD CONSTRAINT novel_states_current_scene_id_fkey FOREIGN KEY (current_scene_id) REFERENCES scenes(id) ON DELETE SET NULL;
   END IF;
END $$; 