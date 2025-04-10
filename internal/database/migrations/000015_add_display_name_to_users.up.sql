-- Добавляем поле display_name в таблицу users
ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name VARCHAR(100);

-- Заполняем display_name текущими значениями username для существующих пользователей
UPDATE users SET display_name = username WHERE display_name IS NULL; 