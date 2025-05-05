-- +migrate Down

-- This reverts the function to the state before dynamic language support was added,
-- but keeps the fix for the 'theme' alias collision.

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
    -- Fix: Changed alias from 'theme' to 'element_text' to avoid column name collision
    SELECT string_agg(element_text, ' ') INTO v_themes FROM jsonb_array_elements_text(v_config->'pp'->'th') AS element_text;
    NEW.search_genre := v_genre;
    NEW.search_franchise := v_franchise;
    NEW.search_themes := COALESCE(v_themes, '');

    -- Extract data from setup JSONB (characters)
    SELECT string_agg(COALESCE(c->>'n', '') || ' ' || COALESCE(c->>'d', '') || ' ' || COALESCE(c->>'p', ''), ' ')
    INTO v_char_details
    FROM jsonb_array_elements(v_setup->'chars');
    NEW.search_characters := COALESCE(v_char_details, '');

    -- Update the tsvector column. Use 'russian' config (hardcoded).
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

-- +migrate StatementEnd 