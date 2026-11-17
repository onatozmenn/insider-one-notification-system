CREATE INDEX idx_notifications_queued_updated
    ON notifications (updated_at)
    WHERE status = 'queued';
