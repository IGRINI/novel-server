-- Remove the internal_generation_step column
ALTER TABLE published_stories
DROP COLUMN IF EXISTS internal_generation_step;

-- Drop the ENUM type
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_type WHERE typname = 'internal_generation_step') THEN
        DROP TYPE internal_generation_step;
--        RAISE NOTICE 'ENUM type internal_generation_step dropped.';
--    ELSE
--        RAISE NOTICE 'ENUM type internal_generation_step does not exist, skipping drop.';
    END IF;
END
$$;

--RAISE NOTICE 'Migration 000013 DOWN applied: internal_generation_step column and type dropped.'; 