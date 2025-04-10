-- Удаляем таблицу novel_likes
DROP TABLE IF EXISTS novel_likes;

-- Удаляем колонку like_count из таблицы novels
ALTER TABLE novels DROP COLUMN IF EXISTS like_count; 