CREATE TABLE templates (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 100),
    channel text NOT NULL CHECK (channel IN ('sms', 'email', 'push')),
    subject text,
    content text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT templates_subject_rule CHECK (
        (channel = 'sms' AND subject IS NULL)
        OR (channel = 'email' AND subject IS NOT NULL AND char_length(subject) BETWEEN 1 AND 200)
        OR (channel = 'push' AND (subject IS NULL OR char_length(subject) <= 100))
    ),
    CONSTRAINT templates_content_length CHECK (
        (channel = 'sms' AND char_length(content) <= 160)
        OR (channel = 'email' AND char_length(content) <= 50000)
        OR (channel = 'push' AND char_length(content) <= 500)
    )
);

CREATE INDEX idx_templates_channel ON templates (channel);
