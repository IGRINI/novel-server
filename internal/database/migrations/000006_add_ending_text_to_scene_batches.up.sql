-- Добавляем колонку для текста концовки
ALTER TABLE scene_batches ADD COLUMN ending_text TEXT NULL;

-- (Опционально, но рекомендуется) Добавляем CHECK constraint, чтобы гарантировать, что заполнено либо choices, либо ending_text
ALTER TABLE scene_batches ADD CONSTRAINT check_choices_or_ending
CHECK ( (choices IS NOT NULL AND ending_text IS NULL) OR (choices IS NULL AND ending_text IS NOT NULL) ); 