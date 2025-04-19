-- Removes the input_data column from generation_results

ALTER TABLE generation_results
DROP COLUMN IF EXISTS input_data; 