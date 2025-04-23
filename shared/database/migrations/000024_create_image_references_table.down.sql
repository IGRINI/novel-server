-- Удаляем триггер (если он существует)
DROP TRIGGER IF EXISTS set_timestamp_image_references ON image_references;

-- Функцию trigger_set_timestamp() не удаляем, т.к. она может использоваться другими таблицами.
-- Если она используется только здесь, можно добавить:
-- DROP FUNCTION IF EXISTS trigger_set_timestamp();

-- Удаляем таблицу (если она существует)
DROP TABLE IF EXISTS image_references; 