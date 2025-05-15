ALTER TABLE published_stories
ALTER COLUMN pending_card_img_tasks DROP DEFAULT IF EXISTS;

-- Тип уже INTEGER, поэтому ALTER COLUMN TYPE ... USING ... не нужен.
-- Просто убедимся, что колонка NOT NULL и установим DEFAULT.

ALTER TABLE published_stories
ALTER COLUMN pending_card_img_tasks SET DEFAULT 0;

-- Если колонка могла быть NULLABLE и должна стать NOT NULL:
-- ALTER TABLE published_stories ALTER COLUMN pending_card_img_tasks SET NOT NULL;
-- Однако, если она изначально создавалась как INTEGER NOT NULL DEFAULT 0 (согласно миграции 000005),
-- то она уже должна быть NOT NULL. Проверяем и устанавливаем на всякий случай, 
-- если предыдущее состояние было другим (например, boolean nullable).
-- Если она уже NOT NULL, эта команда ничего не изменит или выполнится успешно.
ALTER TABLE published_stories ALTER COLUMN pending_card_img_tasks SET NOT NULL;

-- NOT NULL constraint should already be there from the boolean definition if it was boolean before.
-- PG preserves NOT NULL on ALTER TYPE if possible. 