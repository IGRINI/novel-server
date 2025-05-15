ALTER TABLE published_stories
ALTER COLUMN pending_card_img_tasks DROP DEFAULT;

ALTER TABLE published_stories
ALTER COLUMN pending_card_img_tasks TYPE BOOLEAN
USING CASE WHEN pending_card_img_tasks != 0 THEN TRUE ELSE FALSE END;

ALTER TABLE published_stories
ALTER COLUMN pending_card_img_tasks SET DEFAULT FALSE; 