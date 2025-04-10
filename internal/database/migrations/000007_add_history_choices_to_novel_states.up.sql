ALTER TABLE novel_states
    ADD COLUMN IF NOT EXISTS history_choices JSONB NOT NULL DEFAULT '[]'::JSONB;

COMMENT ON COLUMN novel_states.history_choices IS 'История предложенных вариантов выбора для каждого батча'; 