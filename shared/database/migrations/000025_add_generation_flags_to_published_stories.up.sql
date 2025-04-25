-- Добавляем флаги для отслеживания параллельной генерации первой сцены и изображений
ALTER TABLE published_stories
ADD COLUMN is_first_scene_pending BOOLEAN NOT NULL DEFAULT FALSE,
ADD COLUMN are_images_pending BOOLEAN NOT NULL DEFAULT FALSE;

-- Опционально: Можно установить начальные значения для существующих записей,
-- но пока оставим их FALSE, т.к. старые записи, вероятно, уже в 'ready' или 'error'
-- UPDATE published_stories SET is_first_scene_pending = (status = 'first_scene_pending');
-- UPDATE published_stories SET are_images_pending = FALSE; -- Предполагаем, что для старых записей изображения не генерируются или уже есть

COMMENT ON COLUMN published_stories.is_first_scene_pending IS 'Флаг: True, если генерация первой сцены ожидается или идет.';
COMMENT ON COLUMN published_stories.are_images_pending IS 'Флаг: True, если генерация превью или изображений персонажей ожидается или идет.'; 