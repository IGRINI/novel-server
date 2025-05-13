ALTER TABLE published_stories
ADD COLUMN pending_char_gen_tasks BOOLEAN NOT NULL DEFAULT FALSE,
ADD COLUMN pending_card_img_tasks INTEGER NOT NULL DEFAULT 0,
ADD COLUMN pending_char_img_tasks INTEGER NOT NULL DEFAULT 0; 