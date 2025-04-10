-- Удаляем колонку history_choices из таблицы novel_states
ALTER TABLE novel_states DROP COLUMN IF EXISTS history_choices; 