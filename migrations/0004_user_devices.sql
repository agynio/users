CREATE TABLE user_devices (
    id                   UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    identity_id          UUID        NOT NULL REFERENCES users(identity_id) ON DELETE CASCADE,
    name                 TEXT        NOT NULL,
    openziti_identity_id TEXT        NOT NULL,
    enrollment_jwt       TEXT,
    status               TEXT        NOT NULL DEFAULT 'pending',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX user_devices_identity_id_idx ON user_devices (identity_id);
