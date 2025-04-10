ALTER TABLE novel_states
    ADD COLUMN IF NOT EXISTS core_stats JSONB NOT NULL DEFAULT '{}'::JSONB,
    ADD COLUMN IF NOT EXISTS global_flags JSONB NOT NULL DEFAULT '[]'::JSONB;

COMMENT ON COLUMN novel_states.core_stats IS 'Текущие статы (восстановлено)';
COMMENT ON COLUMN novel_states.global_flags IS 'Глобальные флаги (восстановлено)';
COMMENT ON TABLE novel_states IS 'Восстановлены колонки core_stats и global_flags.'; 