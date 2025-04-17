-- Добавляем колонку display_name
ALTER TABLE users
ADD COLUMN display_name VARCHAR(255);

-- Обновляем display_name для существующих пользователей, устанавливая его равным username
UPDATE users
SET display_name = username
WHERE display_name IS NULL; -- На случай, если колонка уже была добавлена вручную

-- Делаем колонку NOT NULL, так как она всегда должна иметь значение
ALTER TABLE users
ALTER COLUMN display_name SET NOT NULL;

-- Добавляем комментарий к колонке
COMMENT ON COLUMN users.display_name IS 'Отображаемое имя пользователя'; 