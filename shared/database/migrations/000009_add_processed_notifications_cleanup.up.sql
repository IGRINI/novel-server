-- +migrate Up
CREATE OR REPLACE FUNCTION cleanup_processed_notifications()
RETURNS trigger AS $$
BEGIN
    DELETE FROM processed_notifications
    WHERE processed_at < now() - INTERVAL '1 day';
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_cleanup_processed_notifications
AFTER INSERT ON processed_notifications
FOR EACH STATEMENT
EXECUTE FUNCTION cleanup_processed_notifications(); 