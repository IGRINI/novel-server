-- Удаление индексов
DROP INDEX IF EXISTS idx_novel_states_novel_id;
DROP INDEX IF EXISTS idx_novel_states_user_id;
DROP INDEX IF EXISTS idx_choices_scene_id;
DROP INDEX IF EXISTS idx_scenes_order;
DROP INDEX IF EXISTS idx_scenes_novel_id;
DROP INDEX IF EXISTS idx_novels_is_public;
DROP INDEX IF EXISTS idx_novels_author_id;

-- Удаление таблиц
DROP TABLE IF EXISTS novel_states;
DROP TABLE IF EXISTS choices;
DROP TABLE IF EXISTS scenes;
DROP TABLE IF EXISTS novels;
DROP TABLE IF EXISTS users; 