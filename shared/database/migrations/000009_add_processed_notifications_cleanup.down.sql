-- +migrate Down
DROP TRIGGER IF EXISTS trigger_cleanup_processed_notifications ON processed_notifications;
DROP FUNCTION IF EXISTS cleanup_processed_notifications(); 