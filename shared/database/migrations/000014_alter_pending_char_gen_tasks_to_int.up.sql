ALTER TABLE published_stories
ALTER COLUMN pending_char_gen_tasks DROP DEFAULT;

ALTER TABLE published_stories
ALTER COLUMN pending_char_gen_tasks TYPE INTEGER
USING CASE
    WHEN pg_typeof(pending_char_gen_tasks) = 'boolean'::regtype THEN
        CASE WHEN pending_char_gen_tasks::boolean THEN 1 ELSE 0 END
    ELSE pending_char_gen_tasks::integer  -- Если уже integer или другой числовой тип, или можно привести
END;

ALTER TABLE published_stories
ALTER COLUMN pending_char_gen_tasks SET DEFAULT 0;

-- Если колонка была BOOLEAN NOT NULL, она должна остаться INTEGER NOT NULL.
-- Команда SET NOT NULL не вызовет ошибки, если колонка уже NOT NULL.
ALTER TABLE published_stories ALTER COLUMN pending_char_gen_tasks SET NOT NULL;

-- NOT NULL constraint should already be there from the boolean definition
-- If it was, for example, NULLABLE before and needs to be NOT NULL:
-- ALTER TABLE published_stories ALTER COLUMN pending_char_gen_tasks SET NOT NULL;
-- However, based on 000005_add_pending_task_counters.up.sql, it was NOT NULL DEFAULT FALSE.
-- So, it should remain NOT NULL. PG preserves NOT NULL on ALTER TYPE if possible. 