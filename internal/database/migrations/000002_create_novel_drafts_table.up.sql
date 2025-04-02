CREATE TABLE IF NOT EXISTS novel_drafts (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    config JSONB NOT NULL,
    user_prompt TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_novel_drafts_user_id ON novel_drafts (user_id);