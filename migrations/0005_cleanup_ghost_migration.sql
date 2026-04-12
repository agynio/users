-- Remove ghost schema_migrations entry left by the 0003 filename rename.
-- Databases upgraded from v0.3.0–v0.4.1 (which had 0003_add_nickname.sql)
-- to v0.5.0+ (which has 0003_add_email.sql) retain the old entry.
DELETE FROM schema_migrations WHERE version = '0003_add_nickname.sql';
