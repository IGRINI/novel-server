-- Удаляем флаги отслеживания параллельной генерации
ALTER TABLE published_stories
DROP COLUMN IF EXISTS is_first_scene_pending,
DROP COLUMN IF EXISTS are_images_pending; 