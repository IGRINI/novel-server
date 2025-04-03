-- Удаляем CHECK constraint
ALTER TABLE scene_batches DROP CONSTRAINT IF EXISTS check_choices_or_ending;

-- Удаляем колонку для текста концовки
ALTER TABLE scene_batches DROP COLUMN IF EXISTS ending_text; 