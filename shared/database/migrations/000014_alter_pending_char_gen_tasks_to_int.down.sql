ALTER TABLE published_stories
ALTER COLUMN pending_char_gen_tasks TYPE BOOLEAN
USING (pending_char_gen_tasks != 0);

ALTER TABLE published_stories
ALTER COLUMN pending_char_gen_tasks SET DEFAULT FALSE;

-- NOT NULL constraint should be preserved.
-- The original boolean column was NOT NULL DEFAULT FALSE.
-- The integer column became NOT NULL DEFAULT 0.
-- So, reverting to BOOLEAN should also result in NOT NULL DEFAULT FALSE. 