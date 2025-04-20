-- +migrate Up
-- Добавляем недостающие статусы в story_status
ALTER TYPE story_status ADD VALUE IF NOT EXISTS 'game_over_pending';
ALTER TYPE story_status ADD VALUE IF NOT EXISTS 'setup_generating';
ALTER TYPE story_status ADD VALUE IF NOT EXISTS 'completed';
ALTER TYPE story_status ADD VALUE IF NOT EXISTS 'draft';
ALTER TYPE story_status ADD VALUE IF NOT EXISTS 'generating'; 