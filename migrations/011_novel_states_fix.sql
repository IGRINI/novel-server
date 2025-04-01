-- +migrate Up

-- Перед изменением структуры сохраним данные сетапов в таблицу novels
-- Это позволит не потерять сцены при изменении схемы
UPDATE novels n
SET setup_state_data = (
    SELECT state_data
    FROM novel_states ns
    WHERE ns.novel_id = n.novel_id
    AND ns.scene_index = 0
    ORDER BY ns.created_at ASC
    LIMIT 1
)
WHERE setup_state_data IS NULL
AND EXISTS (
    SELECT 1
    FROM novel_states ns
    WHERE ns.novel_id = n.novel_id
    AND ns.scene_index = 0
);

-- Создаем новую временную таблицу с правильной структурой
CREATE TABLE novel_states_new (
    novel_id UUID NOT NULL REFERENCES novels(novel_id) ON DELETE CASCADE,
    scene_index INTEGER NOT NULL, -- Разрешаем любые значения, включая 0
    user_id VARCHAR(255) NOT NULL,  -- Оставляем как информационное поле, но не в PK
    state_hash VARCHAR(255) NOT NULL,
    state_data JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (novel_id, scene_index, state_hash)
);

-- Копируем данные, удаляя дубликаты
INSERT INTO novel_states_new (novel_id, scene_index, user_id, state_hash, state_data, created_at, updated_at)
SELECT DISTINCT ON (novel_id, scene_index, state_hash) 
    novel_id, scene_index, user_id, state_hash, state_data, created_at, updated_at
FROM novel_states
ORDER BY novel_id, scene_index, state_hash, created_at ASC;

-- Удаляем старую таблицу
DROP TABLE novel_states;

-- Переименовываем новую таблицу
ALTER TABLE novel_states_new RENAME TO novel_states;

-- Создаем индексы для быстрого поиска
CREATE INDEX idx_novel_states_hash ON novel_states(state_hash);
CREATE INDEX idx_novel_states_scene ON novel_states(novel_id, scene_index);
CREATE INDEX idx_novel_states_user ON novel_states(user_id, novel_id);

-- +migrate Down

-- Для отката нам нужно восстановить оригинальную структуру таблицы
CREATE TABLE novel_states_original (
    novel_id UUID NOT NULL REFERENCES novels(novel_id) ON DELETE CASCADE,
    scene_index INTEGER NOT NULL, -- Убираем проверку scene_index > 0 для отката
    user_id VARCHAR(255) NOT NULL,
    state_hash VARCHAR(255) NOT NULL,
    state_data JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (novel_id, scene_index, state_hash)
);

-- Копируем данные обратно
INSERT INTO novel_states_original (novel_id, scene_index, user_id, state_hash, state_data, created_at, updated_at)
SELECT novel_id, scene_index, user_id, state_hash, state_data, created_at, updated_at
FROM novel_states;

-- Восстанавливаем сетапы из novels.setup_state_data
INSERT INTO novel_states_original (novel_id, scene_index, user_id, state_hash, state_data, created_at, updated_at)
SELECT 
    novel_id, 
    0 as scene_index,  -- Устанавливаем сцену 0 для сетапа
    user_id,  -- Берем user_id из самой ранней сцены пользователя
    MD5(setup_state_data::text) as state_hash,  -- Генерируем хеш из содержимого
    setup_state_data,
    created_at,
    updated_at
FROM novels n
CROSS JOIN LATERAL (
    SELECT user_id FROM novel_states 
    WHERE novel_id = n.novel_id 
    ORDER BY created_at ASC LIMIT 1
) u
WHERE setup_state_data IS NOT NULL;

-- Удаляем текущую таблицу
DROP TABLE novel_states;

-- Переименовываем исходную таблицу
ALTER TABLE novel_states_original RENAME TO novel_states;

-- Восстанавливаем индексы
CREATE INDEX idx_novel_states_hash ON novel_states(state_hash);
CREATE INDEX idx_novel_states_scene ON novel_states(novel_id, scene_index);
CREATE INDEX idx_novel_states_user ON novel_states(user_id, novel_id); 