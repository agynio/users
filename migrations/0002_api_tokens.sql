CREATE TABLE user_api_tokens (
    id            UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    identity_id   UUID        NOT NULL REFERENCES users(identity_id) ON DELETE CASCADE,
    name          TEXT        NOT NULL,
    token_hash    TEXT        NOT NULL,
    token_prefix  TEXT        NOT NULL,
    expires_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at  TIMESTAMPTZ
);

CREATE UNIQUE INDEX user_api_tokens_token_hash_idx ON user_api_tokens (token_hash);
CREATE INDEX user_api_tokens_identity_id_created_at_idx ON user_api_tokens (identity_id, created_at);
