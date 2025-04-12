-- +migrate Down
-- Down Migration: Drop generation_results table

DROP INDEX IF EXISTS idx_generation_results_prompt_type;
DROP INDEX IF EXISTS idx_generation_results_created_at;
DROP INDEX IF EXISTS idx_generation_results_user_id;
DROP TABLE IF EXISTS generation_results; 