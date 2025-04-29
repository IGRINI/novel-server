-- +migrate Down
-- Remove columns for token counts and estimated cost from the generation_results table.

ALTER TABLE generation_results
DROP COLUMN IF EXISTS estimated_cost_usd,
DROP COLUMN IF EXISTS completion_tokens,
DROP COLUMN IF EXISTS prompt_tokens; 