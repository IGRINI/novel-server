-- +migrate Up
ALTER TYPE story_status ADD VALUE IF NOT EXISTS 'moderation_pending';
ALTER TYPE story_status ADD VALUE IF NOT EXISTS 'protagonist_goal_pending';
ALTER TYPE story_status ADD VALUE IF NOT EXISTS 'scene_planner_pending';

-- +migrate StatementEnd 