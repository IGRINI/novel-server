-- +migrate Up
-- Add columns for token counts and estimated cost to the generation_results table.

ALTER TABLE generation_results
ADD COLUMN prompt_tokens INTEGER NOT NULL DEFAULT 0,
ADD COLUMN completion_tokens INTEGER NOT NULL DEFAULT 0,
ADD COLUMN estimated_cost_usd NUMERIC(12, 8) NOT NULL DEFAULT 0.0;

COMMENT ON COLUMN generation_results.prompt_tokens IS 'Количество токенов в промпте запроса к AI.';
COMMENT ON COLUMN generation_results.completion_tokens IS 'Количество токенов в ответе AI.';
COMMENT ON COLUMN generation_results.estimated_cost_usd IS 'Примерная стоимость запроса к AI в USD.'; 