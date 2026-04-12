# Contributing

## Database migrations

- Migration files in `migrations/` are immutable once merged or released
  (filename and contents).
- Never edit, rename, or reorder existing migrations; renaming old migrations
  is prohibited.
- If behavior must change, add a new migration with the next sequence number.
