ALTER TABLE scene_batches DROP CONSTRAINT IF EXISTS check_batch_or_ending;
ALTER TABLE scene_batches ADD CONSTRAINT check_choices_or_ending
    CHECK ((choices IS NOT NULL AND ending_text IS NULL) OR (choices IS NULL AND ending_text IS NOT NULL)); 