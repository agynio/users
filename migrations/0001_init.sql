CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE TABLE users (
    identity_id  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    oidc_subject TEXT NOT NULL,
    name         TEXT NOT NULL DEFAULT '',
    nickname     TEXT NOT NULL DEFAULT '',
    photo_url    TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX users_oidc_subject_idx ON users (oidc_subject);
