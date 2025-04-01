-- +migrate Up
-- Добавляем столбец short_description в таблицу novels, если его нет
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'novels' AND column_name = 'short_description'
    ) THEN
        ALTER TABLE novels ADD COLUMN short_description VARCHAR(500);
        
        -- Обновляем существующие записи, используя данные из config_data
        UPDATE novels 
        SET short_description = COALESCE(
            (config_data->>'short_description')::varchar,
            'Описание недоступно'
        );
    END IF;
END
$$;

-- Добавляем столбец updated_at в таблицу novel_states, если его нет
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'novel_states' AND column_name = 'updated_at'
    ) THEN
        ALTER TABLE novel_states ADD COLUMN updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP;
        
        -- Устанавливаем updated_at равным created_at для существующих записей
        UPDATE novel_states 
        SET updated_at = created_at;
    END IF;
END
$$;

-- +migrate Down
-- Удаляем столбец short_description из таблицы novels, если он существует
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'novels' AND column_name = 'short_description'
    ) THEN
        ALTER TABLE novels DROP COLUMN short_description;
    END IF;
END
$$;

-- Удаляем столбец updated_at из таблицы novel_states, если он существует
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'novel_states' AND column_name = 'updated_at'
    ) THEN
        ALTER TABLE novel_states DROP COLUMN updated_at;
    END IF;
END
$$; 