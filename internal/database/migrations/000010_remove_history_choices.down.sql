-- Возвращаем колонку history_choices в таблицу novel_states
ALTER TABLE novel_states ADD COLUMN IF NOT EXISTS history_choices JSONB;

COMMENT ON COLUMN novel_states.history_choices IS 'История выборов, доступных игроку в каждом батче'; 