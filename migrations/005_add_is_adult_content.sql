-- +migrate Up
-- Добавляем столбец is_adult_content в таблицу novels
ALTER TABLE novels
ADD COLUMN is_adult_content BOOLEAN NOT NULL DEFAULT FALSE;

-- Опционально: можно проставить TRUE для существующих новелл на основе каких-то критериев,
-- но по умолчанию все будут FALSE.
-- Пример:
-- UPDATE novels SET is_adult_content = TRUE WHERE genre = 'Erotica';

-- +migrate Down
-- Удаляем столбец is_adult_content из таблицы novels
ALTER TABLE novels
DROP COLUMN IF EXISTS is_adult_content;
