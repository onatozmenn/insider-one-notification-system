CREATE TABLE batches (
    id uuid PRIMARY KEY,
    total_count integer NOT NULL CHECK (total_count BETWEEN 1 AND 1000),
    idempotency_key text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE notifications (
    id uuid PRIMARY KEY,
    batch_id uuid REFERENCES batches(id) ON DELETE CASCADE,
    recipient text NOT NULL CHECK (char_length(recipient) BETWEEN 1 AND 512),
    channel text NOT NULL CHECK (channel IN ('sms', 'email', 'push')),
    subject text,
    content text NOT NULL,
    priority text NOT NULL DEFAULT 'normal' CHECK (priority IN ('high', 'normal', 'low')),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN (
        'pending', 'scheduled', 'queued', 'processing',
        'accepted', 'failed', 'cancelled', 'dead_letter', 'delivered'
    )),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts >= 1),
    scheduled_at timestamptz,
    next_attempt_at timestamptz,
    provider_message_id text,
    last_error text,
    correlation_id text NOT NULL CHECK (char_length(correlation_id) > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    accepted_at timestamptz,
    cancelled_at timestamptz,
    CONSTRAINT notifications_attempts_within_limit CHECK (attempt_count <= max_attempts),
    CONSTRAINT notifications_scheduled_future CHECK (scheduled_at IS NULL OR scheduled_at > created_at),
    CONSTRAINT notifications_sms_no_subject CHECK (channel <> 'sms' OR subject IS NULL),
    CONSTRAINT notifications_email_subject CHECK (channel <> 'email' OR subject IS NOT NULL),
    CONSTRAINT notifications_subject_length CHECK (
        subject IS NULL
        OR (channel = 'email' AND char_length(subject) <= 200)
        OR (channel = 'push' AND char_length(subject) <= 100)
    ),
    CONSTRAINT notifications_content_length CHECK (
        (channel = 'sms' AND char_length(content) <= 160)
        OR (channel = 'email' AND char_length(content) <= 50000)
        OR (channel = 'push' AND char_length(content) <= 500)
    )
);

CREATE INDEX idx_notifications_status_created
    ON notifications (status, created_at DESC);

CREATE INDEX idx_notifications_channel_created
    ON notifications (channel, created_at DESC);

CREATE INDEX idx_notifications_batch_created
    ON notifications (batch_id, created_at DESC);

CREATE INDEX idx_notifications_created
    ON notifications (created_at DESC, id DESC);

CREATE INDEX idx_notifications_active
    ON notifications (status)
    WHERE status IN ('pending', 'scheduled', 'queued', 'processing');

CREATE INDEX idx_notifications_scheduled
    ON notifications (scheduled_at)
    WHERE status = 'scheduled';

CREATE TABLE notification_events (
    id bigserial PRIMARY KEY,
    notification_id uuid NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    status text NOT NULL,
    detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_notification_events_notification
    ON notification_events (notification_id, created_at);

CREATE TABLE idempotency_keys (
    scope text NOT NULL,
    key text NOT NULL,
    request_hash text NOT NULL,
    state text NOT NULL CHECK (state IN ('in_progress', 'completed')),
    resource_id uuid,
    response_status integer,
    response_body jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT now() + interval '24 hours',
    PRIMARY KEY (scope, key)
);

CREATE INDEX idx_idempotency_expires ON idempotency_keys (expires_at);
