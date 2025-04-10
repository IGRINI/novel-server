ALTER TABLE novel_states
    RENAME COLUMN variables TO story_variables;

COMMENT ON COLUMN novel_states.story_variables IS 'Переменные сюжета, включая core_stats и global_flags'; 