-- NOTE: Filename is legacy ("add_email") but this migration adds nickname.
-- Keep bootstrap re-apply idempotent when nickname already exists.
ALTER TABLE users ADD COLUMN IF NOT EXISTS nickname TEXT NOT NULL DEFAULT '';
