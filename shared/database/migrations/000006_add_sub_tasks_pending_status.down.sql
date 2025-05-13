-- Temporarily rename the current enum type (which includes 'sub_tasks_pending')
ALTER TYPE story_status RENAME TO story_status_temp_for_downgrade;

-- Recreate the enum type as it was BEFORE adding 'sub_tasks_pending'
-- This list should match the state after 000004_add_new_story_statuses.up.sql
CREATE TYPE story_status AS ENUM (
    'draft',
    'moderation_pending',
    'protagonist_goal_pending',
    'scene_planner_pending',
    'setup_pending',
    'image_generation_pending',
    'first_scene_pending',
    'initial_generation',
    'generating',
    'ready',
    'error'
);

-- Update existing tables to use the recreated enum type.
-- Важно: если в таблицах есть записи со статусом 'sub_tasks_pending',
-- этот каст вызовет ошибку. Перед запуском down-миграции нужно
-- либо удалить такие записи, либо обновить их статус на один из существующих.
-- Для простоты, здесь предполагается, что таких записей нет или они обработаны.
ALTER TABLE published_stories ALTER COLUMN status DROP DEFAULT;
ALTER TABLE published_stories ALTER COLUMN status TYPE story_status USING status::text::story_status;
ALTER TABLE published_stories ALTER COLUMN status SET DEFAULT 'setup_pending'::story_status;
-- Если есть другие таблицы, использующие story_status, их также нужно обновить здесь.

-- Drop the temporary enum type
DROP TYPE story_status_temp_for_downgrade; 