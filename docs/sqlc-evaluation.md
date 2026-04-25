# sqlc Adoption

Status: adopted.

## Context

The cache layer has stable SQLite upserts, filtered list queries, stats, and
clear operations. `sqlc` lets us keep SQL as the source of truth, generate
type-safe Go adapters, and catch query/schema drift during local checks.

## Decision

Use `sqlc` for cache schema-aware queries:

- `internal/db/schema.sql` owns the SQLite schema used for code generation.
- `internal/db/query.sql` owns upserts, filtered list queries, stats, and clear.
- Generated Go code lives beside those SQL files in `internal/db`.
- `Cache` remains the public adapter so CLI code does not depend on generated
  row types.

## Workflow

Regenerate after editing `schema.sql` or `query.sql`:

```bash
make sqlc
```

Verify generated code without mutating the working tree:

```bash
make sqlc-check
```

`make check` runs `sqlc-check`, so generated code drift fails CI-style checks.
