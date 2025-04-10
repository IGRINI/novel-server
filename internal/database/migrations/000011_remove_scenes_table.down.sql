CREATE TABLE IF NOT EXISTS public.scenes (
    id uuid DEFAULT gen_random_uuid() NOT NULL PRIMARY KEY,
    novel_id uuid NOT NULL REFERENCES public.novels(id) ON DELETE CASCADE,
    batch_number integer NOT NULL,
    story_summary_so_far text,
    future_direction text,
    choices jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    UNIQUE(novel_id, batch_number)
); 