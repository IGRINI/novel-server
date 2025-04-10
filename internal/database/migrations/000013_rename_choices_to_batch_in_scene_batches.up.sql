ALTER TABLE scene_batches DROP CONSTRAINT IF EXISTS check_choices_or_ending;
ALTER TABLE scene_batches ADD CONSTRAINT check_batch_or_ending
    CHECK ((batch IS NOT NULL AND ending_text IS NULL) OR (batch IS NULL AND ending_text IS NOT NULL)); 