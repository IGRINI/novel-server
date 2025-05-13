-- +migrate Up
CREATE TABLE processed_notifications (
    task_id TEXT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
); 