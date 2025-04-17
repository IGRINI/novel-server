-- +migrate Up

-- 1. Добавляем временную колонку UUID в users
ALTER TABLE users ADD COLUMN uuid_id UUID DEFAULT gen_random_uuid();

-- 2. Обновляем временную колонку (для существующих пользователей)
-- Так как пользователь один, эта команда отработает быстро.
-- Если бы пользователей было много, это могло бы занять время.
UPDATE users SET uuid_id = gen_random_uuid() WHERE uuid_id IS NULL;

-- 3. Делаем временную колонку NOT NULL
ALTER TABLE users ALTER COLUMN uuid_id SET NOT NULL;

-- 4. Добавляем временные колонки UUID в связанные таблицы
ALTER TABLE story_configs ADD COLUMN user_uuid UUID;
ALTER TABLE published_stories ADD COLUMN user_uuid UUID;
ALTER TABLE player_progress ADD COLUMN user_uuid UUID;
ALTER TABLE generation_results ADD COLUMN user_uuid UUID; -- Здесь тип был VARCHAR, меняем на UUID

-- 5. Обновляем временные колонки UUID в связанных таблицах, используя JOIN по старому ID
UPDATE story_configs sc SET user_uuid = u.uuid_id FROM users u WHERE sc.user_id = u.id;
UPDATE published_stories ps SET user_uuid = u.uuid_id FROM users u WHERE ps.user_id = u.id;
UPDATE player_progress pp SET user_uuid = u.uuid_id FROM users u WHERE pp.user_id = u.id;
-- Обновляем generation_results, приводя старый user_id (VARCHAR) к BIGINT для JOIN
-- ВНИМАНИЕ: Если в generation_results.user_id были нечисловые строки, это вызовет ошибку!
-- Но так как пользователь один и ID числовые, должно сработать.
UPDATE generation_results gr SET user_uuid = u.uuid_id
FROM users u
WHERE gr.user_id = u.id::TEXT; -- Приводим users.id к TEXT для сравнения

-- 6. Удаляем старые Foreign Key Constraints
-- ВНИМАНИЕ: Проверьте и используйте актуальные имена ограничений!
ALTER TABLE story_configs DROP CONSTRAINT fk_user;
ALTER TABLE published_stories DROP CONSTRAINT published_stories_user_id_fkey; -- Пример имени, проверьте свое!
ALTER TABLE player_progress DROP CONSTRAINT player_progress_user_id_fkey; -- Пример имени, проверьте свое!
-- В generation_results внешнего ключа не было

-- 7. Удаляем старый Primary Key Constraint таблицы users
-- ВНИМАНИЕ: Проверьте и используйте актуальное имя ограничения!
ALTER TABLE users DROP CONSTRAINT users_pkey; -- Пример имени, проверьте свое!

-- 8. Удаляем старую колонку id из users
ALTER TABLE users DROP COLUMN id;

-- 9. Переименовываем временную колонку uuid_id в id
ALTER TABLE users RENAME COLUMN uuid_id TO id;

-- 10. Делаем новую колонку id (UUID) первичным ключом
ALTER TABLE users ADD PRIMARY KEY (id);

-- 11. Удаляем старые колонки user_id из связанных таблиц
ALTER TABLE story_configs DROP COLUMN user_id;
ALTER TABLE published_stories DROP COLUMN user_id;
ALTER TABLE player_progress DROP COLUMN user_id;
ALTER TABLE generation_results DROP COLUMN user_id;

-- 12. Переименовываем временные колонки user_uuid в user_id
ALTER TABLE story_configs RENAME COLUMN user_uuid TO user_id;
ALTER TABLE published_stories RENAME COLUMN user_uuid TO user_id;
ALTER TABLE player_progress RENAME COLUMN user_uuid TO user_id;
ALTER TABLE generation_results RENAME COLUMN user_uuid TO user_id;

-- 13. Устанавливаем NOT NULL для новых user_id (где это требуется)
-- (В player_progress user_id часть композитного ключа, он уже NOT NULL неявно)
ALTER TABLE story_configs ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE published_stories ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE generation_results ALTER COLUMN user_id SET NOT NULL; -- Устанавливаем NOT NULL здесь

-- 14. Добавляем новые Foreign Key Constraints
-- ВНИМАНИЕ: Используйте актуальные имена ограничений, если они отличаются!
ALTER TABLE story_configs
ADD CONSTRAINT fk_user FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE published_stories
ADD CONSTRAINT published_stories_user_id_fkey FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE player_progress
ADD CONSTRAINT player_progress_user_id_fkey FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE;

-- 15. Обновляем/пересоздаем индексы (если нужно)
-- Индексы по user_id в связанных таблицах, вероятно, нужно пересоздать,
-- так как тип данных изменился. PostgreSQL может сделать это автоматически,
-- но лучше пересоздать явно для уверенности.
DROP INDEX IF EXISTS idx_story_configs_user_id;
CREATE INDEX idx_story_configs_user_id ON story_configs (user_id);

DROP INDEX IF EXISTS idx_published_stories_user_id;
CREATE INDEX idx_published_stories_user_id ON published_stories (user_id);

-- Для player_progress user_id является частью PK, индекс для PK пересоздается автоматически.

DROP INDEX IF EXISTS idx_generation_results_user_id;
CREATE INDEX idx_generation_results_user_id ON generation_results (user_id); 