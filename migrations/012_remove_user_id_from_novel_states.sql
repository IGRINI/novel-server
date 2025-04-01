-- +migrate Up

-- Создаем временную таблицу без поля user_id
CREATE TABLE novel_states_new (
    novel_id UUID NOT NULL REFERENCES novels(novel_id) ON DELETE CASCADE,
    scene_index INTEGER NOT NULL,
    state_hash VARCHAR(255) NOT NULL,
    state_data JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (novel_id, scene_index, state_hash)
);

-- Копируем данные (берем только уникальные строки по первичному ключу)
INSERT INTO novel_states_new (novel_id, scene_index, state_hash, state_data, created_at, updated_at)
SELECT DISTINCT ON (novel_id, scene_index, state_hash) 
    novel_id, scene_index, state_hash, state_data, created_at, NOW() as updated_at
FROM novel_states;

-- Удаляем старую таблицу
DROP TABLE novel_states;

-- Переименовываем новую таблицу
ALTER TABLE novel_states_new RENAME TO novel_states;

-- Создаем индекс для быстрого поиска по хешу
CREATE INDEX idx_novel_states_state_hash ON novel_states(state_hash);

-- Создаем индекс для быстрого поиска по novel_id и scene_index
CREATE INDEX idx_novel_states_novel_scene ON novel_states(novel_id, scene_index);

-- +migrate Down

-- Создаем временную таблицу с полем user_id
CREATE TABLE novel_states_old (
    novel_id UUID NOT NULL REFERENCES novels(novel_id) ON DELETE CASCADE,
    scene_index INTEGER NOT NULL,
    user_id VARCHAR(255) NOT NULL DEFAULT 'system',
    state_hash VARCHAR(255) NOT NULL,
    state_data JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (novel_id, scene_index, state_hash)
);

-- Копируем данные обратно (добавляем placeholder для user_id)
INSERT INTO novel_states_old (novel_id, scene_index, state_hash, state_data, created_at, updated_at)
SELECT novel_id, scene_index, state_hash, state_data, created_at, updated_at
FROM novel_states;

-- Удаляем новую таблицу
DROP TABLE novel_states;

-- Переименовываем старую таблицу обратно
ALTER TABLE novel_states_old RENAME TO novel_states;

-- Создаем индексы
CREATE INDEX idx_novel_states_state_hash ON novel_states(state_hash);
CREATE INDEX idx_novel_states_novel_scene ON novel_states(novel_id, scene_index);
CREATE INDEX idx_novel_states_user_id ON novel_states(user_id, novel_id); 