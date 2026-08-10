ALTER TABLE user_devices
    ADD COLUMN connectivity TEXT        NOT NULL DEFAULT 'offline',
    ADD COLUMN enrolled_at  TIMESTAMPTZ,
    ADD COLUMN last_seen_at TIMESTAMPTZ;
