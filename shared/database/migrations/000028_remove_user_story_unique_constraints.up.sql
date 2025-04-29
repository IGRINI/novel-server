-- +migrate Up
-- Удаляем уникальный индекс из player_progress, чтобы разрешить несколько записей для user_id + published_story_id
DROP INDEX IF EXISTS idx_player_progress_user_story;

-- Удаляем уникальный индекс из player_game_states, чтобы разрешить несколько состояний игры для player_id + published_story_id
-- Имя индекса совпадает с именем ограничения, созданного в 000022
DROP INDEX IF EXISTS unique_player_story_game_state; 