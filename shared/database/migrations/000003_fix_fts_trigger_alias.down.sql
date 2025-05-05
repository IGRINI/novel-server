-- +migrate Down

-- Reverts the function to the state where the alias was 'c'
-- This is the version with dynamic language support.

CREATE OR REPLACE FUNCTION update_published_story_fts() RETURNS TRIGGER AS $$
DECLARE
    v_author_name TEXT;
    v_genre TEXT;
    v_franchise TEXT;
    v_themes TEXT;
    v_char_details TEXT;
    v_config JSONB;
    v_setup JSONB;
    v_fts_config REGCONFIG; -- Variable to hold the selected FTS configuration OID
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
    SELECT string_agg(element_text, ' ') INTO v_themes FROM jsonb_array_elements_text(v_config->'pp'->'th') AS element_text;
    NEW.search_genre := v_genre;
    NEW.search_franchise := v_franchise;
    NEW.search_themes := COALESCE(v_themes, '');

    -- Extract data from setup JSONB (characters) - Uses alias 'c'
    SELECT string_agg(COALESCE(c->>'n', '') || ' ' || COALESCE(c->>'d', '') || ' ' || COALESCE(c->>'p', ''), ' ')
    INTO v_char_details
    FROM jsonb_array_elements(v_setup->'chars') AS c; -- Alias is 'c' here
    NEW.search_characters := COALESCE(v_char_details, '');

    -- Determine FTS configuration based on language
    SELECT cfgname::regconfig INTO v_fts_config
    FROM pg_ts_config
    WHERE cfgname = CASE LOWER(NEW.language) -- Use lower case for robust matching
        WHEN 'en' THEN 'english'
        WHEN 'fr' THEN 'french'
        WHEN 'de' THEN 'german'
        WHEN 'es' THEN 'spanish'
        WHEN 'it' THEN 'italian'
        WHEN 'pt' THEN 'portuguese'
        WHEN 'ru' THEN 'russian'
        -- Add other languages if configurations exist (e.g., dutch, swedish, etc.)
        -- For zh and ja, 'simple' is the default fallback unless extensions are installed
        ELSE 'simple'
    END;

    -- If a specific language config wasn't found (not installed), default to simple
    IF v_fts_config IS NULL THEN
        v_fts_config := 'simple'::regconfig;
        -- Consider raising a notice/warning if needed:
        -- RAISE NOTICE 'FTS configuration for language % not found, using simple.', NEW.language;
    END IF;

    -- Update the tsvector column using the determined configuration
    NEW.fts_document := setweight(to_tsvector(v_fts_config, COALESCE(NEW.title, '')), 'A') ||
                        setweight(to_tsvector(v_fts_config, COALESCE(NEW.description, '')), 'B') ||
                        setweight(to_tsvector(v_fts_config, NEW.search_author_name), 'C') ||
                        setweight(to_tsvector(v_fts_config, NEW.search_genre), 'B') ||
                        setweight(to_tsvector(v_fts_config, NEW.search_franchise), 'C') ||
                        setweight(to_tsvector(v_fts_config, NEW.search_themes), 'D') ||
                        setweight(to_tsvector(v_fts_config, NEW.search_characters), 'D');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- +migrate StatementEnd 