-- Consolidated Migration: Creates the final database schema.

-- +migrate Up

-- === Extensions ===
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- === Functions for Timestamps ===

-- Function to set updated_at on update (used by story_configs, image_references)
CREATE OR REPLACE FUNCTION trigger_set_timestamp()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Function to set updated_at on update (used by published_stories, story_scenes, player_progress, prompts, dynamic_configs)
-- Note: This function has the same body as trigger_set_timestamp(). Consider consolidating in the future.
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
   NEW.updated_at = NOW();
   RETURN NEW;
END;
$$ language 'plpgsql';

-- === ENUM Types ===

CREATE TYPE generation_status AS ENUM (
    'pending',
    'generating',
    'draft',
    'error',
    'revising' -- Added in 000012
);

CREATE TYPE story_status AS ENUM (
    'draft',                 -- 000016: Initial status
    'initial_generation',    -- 000023: Changed from generating_scene, now covers setup + first scene
    'ready',                 -- 000017: Ready to be played
    'error',                 -- 000017: Error during generation
    'setup_pending',         -- 000017: Added for setup generation phase
    'image_generation_pending', -- 000031: Added for separate image generation step
    'first_scene_pending',   -- <<< CONSOLIDATED from 000002 >>>
    'generating'             -- <<< CONSOLIDATED from 000003 >>> -- Note: 'generating' from 000003 might conflict/be redundant with 'initial_generation', review usage. Assuming keep for now.
    -- 'generating_scene' WAS HERE (000016, obsolete)
    -- 'setup_generating' WAS HERE (000017, removed)
    -- 'game_over_pending' WAS HERE (000022, obsolete)
);

CREATE TYPE player_game_status AS ENUM (
    'playing',
    'generating_scene',
    'game_over_pending',
    'completed',
    'error'
);

-- === Tables ===

-- Users Table (ID changed to UUID in 000011)
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(), -- Changed from BIGSERIAL
    username VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    email VARCHAR(255) UNIQUE,                         -- Added in 000002
    roles TEXT[] NOT NULL DEFAULT '{ROLE_USER}',       -- Added in 000008
    is_banned BOOLEAN NOT NULL DEFAULT FALSE,          -- Added in 000009
    display_name VARCHAR(255) NOT NULL,                -- Added in 000010
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
COMMENT ON COLUMN users.display_name IS 'Отображаемое имя пользователя';

-- Generation Results Table (user_id changed to UUID in 000011, input_data removed in 000015, cost/tokens added in 000027)
CREATE TABLE IF NOT EXISTS generation_results (
    id TEXT PRIMARY KEY,
    user_id UUID NOT NULL,                           -- Changed from VARCHAR
    prompt_type VARCHAR(100) NOT NULL,
    generated_text TEXT,
    processing_time_ms BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMPTZ NOT NULL,
    error TEXT,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,        -- Added in 000027
    completion_tokens INTEGER NOT NULL DEFAULT 0,    -- Added in 000027
    estimated_cost_usd NUMERIC(12, 8) NOT NULL DEFAULT 0.0 -- Added in 000027
);
COMMENT ON TABLE generation_results IS 'Хранит результаты выполнения задач генерации текста AI.';
COMMENT ON COLUMN generation_results.id IS 'Уникальный идентификатор задачи генерации (обычно совпадает с TaskID из сообщения).';
COMMENT ON COLUMN generation_results.user_id IS 'Идентификатор пользователя, инициировавшего задачу.';
COMMENT ON COLUMN generation_results.prompt_type IS 'Тип промта, использованного для генерации.';
COMMENT ON COLUMN generation_results.generated_text IS 'Текст, сгенерированный AI.';
COMMENT ON COLUMN generation_results.processing_time_ms IS 'Время выполнения AI-запроса и внутренней обработки в миллисекундах.';
COMMENT ON COLUMN generation_results.created_at IS 'Время получения задачи воркером.';
COMMENT ON COLUMN generation_results.completed_at IS 'Время завершения обработки задачи воркером.';
COMMENT ON COLUMN generation_results.error IS 'Текст ошибки, возникшей при обработке задачи (если была).';
COMMENT ON COLUMN generation_results.prompt_tokens IS 'Количество токенов в промпте запроса к AI.';
COMMENT ON COLUMN generation_results.completion_tokens IS 'Количество токенов в ответе AI.';
COMMENT ON COLUMN generation_results.estimated_cost_usd IS 'Примерная стоимость запроса к AI в USD.';

-- Story Configs Table (user_id changed to UUID in 000011, language added in 000019)
CREATE TABLE IF NOT EXISTS story_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,                           -- Changed from BIGINT
    title TEXT,
    description TEXT,
    status generation_status NOT NULL DEFAULT 'pending',
    user_input JSONB NOT NULL DEFAULT '[]'::jsonb,
    config JSONB DEFAULT NULL,
    error_details TEXT DEFAULT NULL,
    language VARCHAR(10) NOT NULL DEFAULT 'en',        -- Added in 000019
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_user FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);
COMMENT ON TABLE story_configs IS 'Хранит конфигурации и метаданные историй, создаваемых пользователями.';
COMMENT ON COLUMN story_configs.id IS 'Уникальный идентификатор конфигурации истории (UUID).';
COMMENT ON COLUMN story_configs.user_id IS 'Идентификатор пользователя, создавшего историю.';
COMMENT ON COLUMN story_configs.title IS 'Название истории, извлеченное из последнего сгенерированного JSON конфига.';
COMMENT ON COLUMN story_configs.description IS 'Краткое описание истории, извлеченное из последнего сгенерированного JSON конфига.';
COMMENT ON COLUMN story_configs.status IS 'Текущий статус процесса генерации истории.';
COMMENT ON COLUMN story_configs.user_input IS 'История пользовательских запросов (промптов) в виде JSON массива строк.';
COMMENT ON COLUMN story_configs.config IS 'Полный JSON конфигурации истории, сгенерированный AI (Narrator).';
COMMENT ON COLUMN story_configs.error_details IS 'Текст последней ошибки, возникшей при генерации.';
COMMENT ON COLUMN story_configs.language IS 'Язык конфигурации истории.';
COMMENT ON COLUMN story_configs.created_at IS 'Время создания записи конфигурации.';
COMMENT ON COLUMN story_configs.updated_at IS 'Время последнего обновления записи конфигурации.';
COMMENT ON CONSTRAINT fk_user ON story_configs IS 'Внешний ключ, связывающий с таблицей users.';
COMMENT ON TYPE generation_status IS 'Перечисление возможных статусов генерации конфигурации истории.';

-- Published Stories Table (user_id changed to UUID in 000011, likes added in 000013, flags added in 000025, language from 000005, FTS from 000008)
CREATE TABLE published_stories (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL,                           -- Changed from BIGINT
    config JSONB NOT NULL,
    setup JSONB,
    status story_status NOT NULL DEFAULT 'setup_pending',
    is_public BOOLEAN NOT NULL DEFAULT FALSE,
    is_adult_content BOOLEAN NOT NULL,
    is_first_scene_pending BOOLEAN NOT NULL DEFAULT FALSE, -- Added in 000025
    are_images_pending BOOLEAN NOT NULL DEFAULT FALSE,   -- Added in 000025
    title VARCHAR(255),
    description TEXT,
    error_details TEXT,
    likes_count BIGINT NOT NULL DEFAULT 0,           -- Added in 000013
    language VARCHAR(10) NOT NULL DEFAULT 'en',      -- <<< CONSOLIDATED from 000005 >>>
    search_author_name TEXT,                         -- <<< CONSOLIDATED from 000008 >>>
    search_genre TEXT,                               -- <<< CONSOLIDATED from 000008 >>>
    search_franchise TEXT,                           -- <<< CONSOLIDATED from 000008 >>>
    search_themes TEXT,                              -- <<< CONSOLIDATED from 000008 >>>
    search_characters TEXT,                          -- <<< CONSOLIDATED from 000008 >>>
    fts_document TSVECTOR,                           -- <<< CONSOLIDATED from 000008 >>>
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT published_stories_user_id_fkey FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);
COMMENT ON COLUMN published_stories.is_first_scene_pending IS 'Флаг: True, если генерация первой сцены ожидается или идет.';
COMMENT ON COLUMN published_stories.are_images_pending IS 'Флаг: True, если генерация превью или изображений персонажей ожидается или идет.';
COMMENT ON COLUMN published_stories.language IS 'Язык опубликованной истории (копируется из конфига при публикации).';

-- Story Scenes Table (updated_at added in 000018)
CREATE TABLE story_scenes (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    published_story_id UUID NOT NULL REFERENCES published_stories(id) ON DELETE CASCADE,
    state_hash VARCHAR(64) NOT NULL,
    scene_content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW() -- Added in 000018
);

-- Player Progress Table (Refactored in 000022: added id PK, FKs nullable; scene_index added in 000020; last_* added in 000021; created_at added in 000026; summary from 000004)
CREATE TABLE player_progress (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),             -- Added in 000022
    user_id UUID NULL REFERENCES users(id) ON DELETE CASCADE, -- Changed from BIGINT NOT NULL (FK is defined inline)
    published_story_id UUID NULL REFERENCES published_stories(id) ON DELETE CASCADE, -- Changed from NOT NULL (FK is defined inline)
    current_core_stats JSONB NOT NULL,
    current_story_variables JSONB NOT NULL DEFAULT '{}'::jsonb,
    current_global_flags TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    current_state_hash VARCHAR(64) NOT NULL,
    scene_index INTEGER NOT NULL DEFAULT 1,                  -- Added in 000020
    last_story_summary TEXT NULL,                            -- Added in 000021
    last_future_direction TEXT NULL,                         -- Added in 000021
    last_var_impact_summary TEXT NULL,                       -- Added in 000021
    current_scene_summary TEXT NULL,                         -- <<< CONSOLIDATED from 000004 >>>
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),           -- Added in 000026
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- CONSTRAINT player_progress_user_id_fkey FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE, -- Removed as redundant (inline FK exists)
    CONSTRAINT unique_story_state_hash UNIQUE (published_story_id, current_state_hash) -- Added in 000022
    -- Old composite PK (user_id, published_story_id) removed in 000022
    -- Old unique index idx_player_progress_user_story removed in 000028
);
COMMENT ON COLUMN player_progress.last_story_summary IS 'Последнее краткое изложение сюжета для этого прогресса';
COMMENT ON COLUMN player_progress.last_future_direction IS 'Последнее направление развития сюжета';
COMMENT ON COLUMN player_progress.last_var_impact_summary IS 'Последнее описание влияния переменных';
COMMENT ON COLUMN player_progress.current_scene_summary IS 'Summary of the scene associated with this progress state';

-- Story Likes Table (Added in 000013)
CREATE TABLE story_likes (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- Ensure FK references users.id
    published_story_id UUID NOT NULL REFERENCES published_stories(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
    PRIMARY KEY (user_id, published_story_id)
);

-- User Device Tokens Table (Added in 000014)
CREATE TABLE IF NOT EXISTS user_device_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token TEXT NOT NULL,
    platform VARCHAR(10) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_user_device_token UNIQUE (user_id, token)
);

-- Player Game States Table (Added in 000022, unique constraint removed in 000028)
CREATE TABLE player_game_states (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- Added FK reference
    published_story_id UUID NOT NULL REFERENCES published_stories(id) ON DELETE CASCADE,
    player_progress_id UUID NULL,
    current_scene_id UUID NULL REFERENCES story_scenes(id) ON DELETE SET NULL,
    player_status player_game_status NOT NULL,
    ending_text TEXT NULL,
    error_details TEXT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_activity_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ NULL,
    -- CONSTRAINT unique_player_story_game_state UNIQUE (player_id, published_story_id), -- Removed in 000028
    CONSTRAINT fk_player_progress FOREIGN KEY (player_progress_id) REFERENCES player_progress(id) ON DELETE SET NULL
);

-- Image References Table (Added in 000024)
CREATE TABLE IF NOT EXISTS image_references (
    reference TEXT PRIMARY KEY,
    image_url TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
COMMENT ON TABLE image_references IS 'Stores generated image URLs keyed by a deterministic reference string.';
COMMENT ON COLUMN image_references.reference IS 'Deterministic reference string (e.g., ch_male_adult_fantasy_...)';
COMMENT ON COLUMN image_references.image_url IS 'URL of the generated and stored image.';
COMMENT ON COLUMN image_references.created_at IS 'Timestamp when the reference was first created.';
COMMENT ON COLUMN image_references.updated_at IS 'Timestamp when the reference or URL was last updated.';

-- Prompts Table (Added in 000029)
CREATE TABLE prompts (
    id SERIAL PRIMARY KEY,
    key VARCHAR(255) NOT NULL,
    language VARCHAR(10) NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    UNIQUE (key, language)
);

-- Dynamic Configs Table (Added in 000030)
CREATE TABLE dynamic_configs (
    key VARCHAR(255) PRIMARY KEY,
    value TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL
);

-- === Indexes ===

-- users
CREATE INDEX IF NOT EXISTS idx_users_username ON users (username);
CREATE INDEX IF NOT EXISTS idx_users_is_banned ON users (is_banned); -- Added in 000009

-- generation_results
CREATE INDEX IF NOT EXISTS idx_generation_results_user_id ON generation_results (user_id); -- Type changed to UUID
CREATE INDEX IF NOT EXISTS idx_generation_results_created_at ON generation_results (created_at);
CREATE INDEX IF NOT EXISTS idx_generation_results_prompt_type ON generation_results (prompt_type);

-- story_configs
CREATE INDEX IF NOT EXISTS idx_story_configs_user_id ON story_configs (user_id); -- Type changed to UUID
CREATE INDEX IF NOT EXISTS idx_story_configs_status ON story_configs (status);

-- published_stories
CREATE INDEX IF NOT EXISTS idx_published_stories_user_id ON published_stories(user_id); -- Type changed to UUID
CREATE INDEX IF NOT EXISTS idx_published_stories_status ON published_stories(status);
CREATE INDEX IF NOT EXISTS idx_published_stories_is_public ON published_stories(is_public);
CREATE INDEX IF NOT EXISTS idx_published_stories_fts_document ON published_stories USING GIN(fts_document); -- <<< CONSOLIDATED from 000008 >>>

-- story_scenes
CREATE INDEX IF NOT EXISTS idx_story_scenes_published_story_id ON story_scenes(published_story_id);
CREATE INDEX IF NOT EXISTS idx_story_scenes_state_hash ON story_scenes(state_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_story_scenes_story_hash ON story_scenes(published_story_id, state_hash);

-- player_progress
CREATE INDEX IF NOT EXISTS idx_player_progress_state_hash ON player_progress(current_state_hash);
-- CREATE UNIQUE INDEX IF NOT EXISTS idx_player_progress_user_story ON player_progress (user_id, published_story_id); -- Removed in 000028
CREATE INDEX IF NOT EXISTS idx_player_progress_user_story ON player_progress (user_id, published_story_id); -- <<< CONSOLIDATED from 000006 >>>
CREATE INDEX IF NOT EXISTS idx_player_progress_published_story_id ON player_progress (published_story_id); -- <<< CONSOLIDATED from 000007 >>>


-- story_likes
CREATE INDEX IF NOT EXISTS idx_story_likes_published_story_id ON story_likes(published_story_id); -- Added in 000013, also CONSOLIDATED from 000007
CREATE INDEX IF NOT EXISTS idx_story_likes_user ON story_likes (user_id); -- <<< CONSOLIDATED from 000006 >>>

-- user_device_tokens
CREATE INDEX IF NOT EXISTS idx_user_device_tokens_user_id ON user_device_tokens(user_id); -- Added in 000014
CREATE INDEX IF NOT EXISTS idx_user_device_tokens_token ON user_device_tokens(token); -- Added in 000014

-- player_game_states
CREATE INDEX IF NOT EXISTS idx_player_game_states_player_id ON player_game_states(player_id); -- Added in 000022
CREATE INDEX IF NOT EXISTS idx_player_game_states_published_story_id ON player_game_states(published_story_id); -- Added in 000022
CREATE INDEX IF NOT EXISTS idx_player_game_states_last_activity ON player_game_states(last_activity_at); -- Added in 000022
CREATE INDEX IF NOT EXISTS idx_player_game_states_player_progress_id ON player_game_states(player_progress_id); -- Added in 000022
-- CREATE UNIQUE INDEX IF NOT EXISTS unique_player_story_game_state ON player_game_states (player_id, published_story_id); -- Removed in 000028
CREATE INDEX IF NOT EXISTS idx_player_game_states_player_story ON player_game_states (player_id, published_story_id); -- <<< CONSOLIDATED from 000006 >>>

-- prompts
CREATE INDEX IF NOT EXISTS idx_prompts_key_language ON prompts (key, language); -- Added in 000029

-- === Triggers ===

-- story_configs
CREATE TRIGGER set_story_configs_timestamp
BEFORE UPDATE ON story_configs
FOR EACH ROW
EXECUTE FUNCTION trigger_set_timestamp();

-- published_stories
CREATE TRIGGER update_published_stories_updated_at
BEFORE UPDATE ON published_stories
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

-- story_scenes
CREATE TRIGGER update_story_scenes_updated_at
BEFORE UPDATE ON story_scenes
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column(); -- Added in 000018

-- player_progress
CREATE TRIGGER update_player_progress_updated_at
BEFORE UPDATE ON player_progress
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

-- image_references
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'set_timestamp_image_references') THEN
    CREATE TRIGGER set_timestamp_image_references
    BEFORE UPDATE ON image_references
    FOR EACH ROW
    EXECUTE PROCEDURE trigger_set_timestamp();
  END IF;
END
$$; -- Added in 000024

-- prompts
CREATE TRIGGER update_prompts_updated_at
BEFORE UPDATE ON prompts
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column(); -- Added in 000029

-- dynamic_configs
CREATE TRIGGER update_dynamic_configs_updated_at
BEFORE UPDATE ON dynamic_configs
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column(); -- Added in 000030


-- <<< FTS Trigger Function and Trigger CONSOLIDATED from 000008 >>>
CREATE OR REPLACE FUNCTION update_published_story_fts() RETURNS TRIGGER AS $$
DECLARE
    v_author_name TEXT;
    v_genre TEXT;
    v_franchise TEXT;
    v_themes TEXT;
    v_char_details TEXT;
    v_config JSONB;
    v_setup JSONB;
BEGIN
    -- Get config and setup as JSONB for easier parsing
    v_config := NEW.config::jsonb;
    v_setup := NEW.setup::jsonb;

    -- Get author name
    SELECT display_name INTO v_author_name FROM users WHERE id = NEW.user_id;
    NEW.search_author_name := COALESCE(v_author_name, '');

    -- Extract data from config JSONB
    v_genre := COALESCE(v_config->>'gn', '');
    v_franchise := COALESCE(v_config->>'fr', '');
    SELECT string_agg(theme, ' ') INTO v_themes FROM jsonb_array_elements_text(v_config->'pp'->'th');
    NEW.search_genre := v_genre;
    NEW.search_franchise := v_franchise;
    NEW.search_themes := COALESCE(v_themes, '');

    -- Extract data from setup JSONB (characters)
    SELECT string_agg(COALESCE(c->>'n', '') || ' ' || COALESCE(c->>'d', '') || ' ' || COALESCE(c->>'p', ''), ' ')
    INTO v_char_details
    FROM jsonb_array_elements(v_setup->'chars');
    NEW.search_characters := COALESCE(v_char_details, '');

    -- Update the tsvector column. Use 'russian' config if applicable, otherwise 'simple' might be safer.
    -- Coalesce prevents errors if fields are NULL.
    -- Assign different weights (A, B, C, D) to prioritize fields.
    NEW.fts_document := setweight(to_tsvector('russian', COALESCE(NEW.title, '')), 'A') ||
                        setweight(to_tsvector('russian', COALESCE(NEW.description, '')), 'B') ||
                        setweight(to_tsvector('russian', NEW.search_author_name), 'C') ||
                        setweight(to_tsvector('russian', NEW.search_genre), 'B') ||
                        setweight(to_tsvector('russian', NEW.search_franchise), 'C') ||
                        setweight(to_tsvector('russian', NEW.search_themes), 'D') ||
                        setweight(to_tsvector('russian', NEW.search_characters), 'D');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_update_published_story_fts ON published_stories; -- Drop just in case if exists from previous attempts
CREATE TRIGGER trg_update_published_story_fts
BEFORE INSERT OR UPDATE ON published_stories
FOR EACH ROW EXECUTE FUNCTION update_published_story_fts();


-- +migrate StatementEnd 