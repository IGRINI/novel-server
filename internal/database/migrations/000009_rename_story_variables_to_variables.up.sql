ALTER TABLE novel_states
    RENAME COLUMN story_variables TO variables;

COMMENT ON COLUMN novel_states.variables IS 'Переменные сюжета, включая core_stats и global_flags (ранее story_variables)'; 