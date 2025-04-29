-- Consolidated Migration: Rolls back the final database schema.

-- +migrate Down

-- === Drop Triggers ===
DROP TRIGGER IF EXISTS update_dynamic_configs_updated_at ON dynamic_configs;
DROP TRIGGER IF EXISTS update_prompts_updated_at ON prompts;
DROP TRIGGER IF EXISTS set_timestamp_image_references ON image_references;
DROP TRIGGER IF EXISTS update_player_progress_updated_at ON player_progress;
DROP TRIGGER IF EXISTS update_story_scenes_updated_at ON story_scenes;
DROP TRIGGER IF EXISTS update_published_stories_updated_at ON published_stories;
DROP TRIGGER IF EXISTS set_story_configs_timestamp ON story_configs;

-- === Drop Tables (in reverse order of dependencies) ===
DROP TABLE IF EXISTS player_game_states;
DROP TABLE IF EXISTS story_likes;
DROP TABLE IF EXISTS player_progress; -- Note: player_progress_user_id_fkey dropped implicitly
DROP TABLE IF EXISTS story_scenes;
DROP TABLE IF EXISTS user_device_tokens;
DROP TABLE IF EXISTS story_configs; -- Note: fk_user dropped implicitly
DROP TABLE IF EXISTS published_stories; -- Note: published_stories_user_id_fkey dropped implicitly
DROP TABLE IF EXISTS generation_results;
DROP TABLE IF EXISTS prompts;
DROP TABLE IF EXISTS image_references;
DROP TABLE IF EXISTS dynamic_configs;
DROP TABLE IF EXISTS users;

-- === Drop ENUM Types ===
DROP TYPE IF EXISTS player_game_status;
DROP TYPE IF EXISTS story_status;
DROP TYPE IF EXISTS generation_status;

-- === Drop Functions (Skipped - assumed shared/pre-existing) ===
-- DROP FUNCTION IF EXISTS update_updated_at_column();
-- DROP FUNCTION IF EXISTS trigger_set_timestamp();

-- === Drop Extensions ===
-- DROP EXTENSION IF EXISTS "uuid-ossp"; -- Usually kept unless strictly part of this migration's features

-- +migrate StatementEnd 