-- +migrate Up
-- Удаляем старый PK, если он вдруг остался с неправильным именем или составом
ALTER TABLE player_progress DROP CONSTRAINT IF EXISTS player_progress_pkey;
-- Добавляем правильный композитный PK
ALTER TABLE player_progress ADD CONSTRAINT player_progress_pkey PRIMARY KEY (user_id, published_story_id); 