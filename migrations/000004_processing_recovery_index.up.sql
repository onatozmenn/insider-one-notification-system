CREATE INDEX idx_notifications_processing_updated
    ON notifications (updated_at)
    WHERE status = 'processing';
