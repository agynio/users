-- Keep bootstrap re-apply idempotent when nickname already exists.
ALTER TABLE users ADD COLUMN IF NOT EXISTS nickname TEXT NOT NULL DEFAULT '';
