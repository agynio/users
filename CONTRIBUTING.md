# Contributing

## Database Migrations

Migration files in `migrations/` use a filename-based runner (`internal/db/migrate.go`). Each file is tracked by **filename** in `schema_migrations`. All migrations run in a single transaction.

### Rules

1. **Immutable once merged.** Do not edit, rename, or reorder existing migration files. The filename is the version key — renaming creates a new version that will be applied to databases that already ran the original.

2. **Additive only.** Schema changes to existing tables require a new file with the next sequence number. Never modify an existing migration to add or change columns.

3. **Idempotent DDL where cross-version upgrades are possible.** If a migration may run against a database that already has the described state (e.g., column added by a mutated earlier migration), use `IF NOT EXISTS` / `IF EXISTS` guards.

4. **No data mutations.** Schema migrations must not contain `INSERT`, `UPDATE`, or `DELETE` on application tables.

### History

- `0001_init.sql` was mutated (replaced `nickname` column with `email`) before v0.1.0 and is present in all released tags. This cannot be reverted.
- `0003_add_nickname.sql` was renamed to `0003_add_email.sql` in v0.5.0. The `IF NOT EXISTS` guard was added post-v0.5.0 (`2082a87`).
- `0005_cleanup_ghost_migration.sql` removes the ghost `0003_add_nickname.sql` entry from databases that upgraded through v0.3.0–v0.4.1.
