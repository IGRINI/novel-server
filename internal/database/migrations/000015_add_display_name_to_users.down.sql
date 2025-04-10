-- Удаляем поле display_name из таблицы users
ALTER TABLE users DROP COLUMN IF EXISTS display_name; 