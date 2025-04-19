-- Re-adds the input_data column to generation_results.

ALTER TABLE generation_results
ADD COLUMN input_data JSONB NOT NULL; 