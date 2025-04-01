-- +migrate Up
-- Добавляем user_id в таблицу novel_states
ALTER TABLE novel_states ADD COLUMN user_id VARCHAR(255) NOT NULL DEFAULT '';

-- Заполняем user_id из связанных записей в таблице novels
UPDATE novel_states ns
SET user_id = (
    SELECT n.user_id
    FROM novels n
    WHERE n.novel_id = ns.novel_id
);

-- Изменяем первичный ключ, включая user_id для поддержки разных прогрессов
ALTER TABLE novel_states DROP CONSTRAINT novel_states_pkey;
ALTER TABLE novel_states ADD PRIMARY KEY (novel_id, scene_index, user_id);

-- Индекс для быстрого поиска состояний по пользователю и новелле
CREATE INDEX IF NOT EXISTS idx_novel_states_user_id ON novel_states(user_id, novel_id);

-- +migrate Down
-- Восстанавливаем исходный первичный ключ
ALTER TABLE novel_states DROP CONSTRAINT novel_states_pkey;
ALTER TABLE novel_states ADD PRIMARY KEY (novel_id, scene_index);

-- Удаляем индекс
DROP INDEX IF EXISTS idx_novel_states_user_id;

-- Удаляем колонку user_id
ALTER TABLE novel_states DROP COLUMN user_id; 