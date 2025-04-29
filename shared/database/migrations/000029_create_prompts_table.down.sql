-- Миграция для удаления таблицы prompts
 
DROP TRIGGER IF EXISTS update_prompts_updated_at ON prompts;
DROP FUNCTION IF EXISTS update_updated_at_column();
DROP TABLE IF EXISTS prompts; 