ALTER TABLE novel_states
    DROP COLUMN IF EXISTS core_stats,
    DROP COLUMN IF EXISTS global_flags;

COMMENT ON TABLE novel_states IS 'Удалены избыточные колонки core_stats и global_flags. Эти данные теперь хранятся внутри variables (ранее story_variables).'; 