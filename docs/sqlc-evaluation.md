# sqlc Evaluation

Status: defer adoption for now.

## Context

The cache layer currently has one schema initializer, three upserts, three list
queries, stats, and clear. Tests cover idempotent writes, imports, exports,
summary reads, the HTTP handler, and the fixture-backed e2e path.

`sqlc` is still a good candidate. The original sqlc post describes the core
value clearly: write SQL once, generate type-safe Go, and catch query/schema
drift at generation time. Current sqlc docs also support SQLite generation with
`engine: "sqlite"`.

## Decision

Do not introduce sqlc in this patch. The storage layer is small, and the two
highest-value read paths build optional filters dynamically. A sqlc migration
would add generated code plus adapters while leaving some query composition in
handwritten Go.

This is worth revisiting when one of these becomes true:

- We add migrations or versioned schema changes.
- We add more aggregate queries for billing and runner analysis.
- Query shape churn starts causing scan/order bugs.
- Multiple contributors are editing the cache layer at the same time.

## Migration Shape

When we adopt sqlc, do it as one storage-focused change:

1. Move the current DDL into `internal/db/schema.sql`.
2. Move stable upsert/list/stats/clear queries into `internal/db/query.sql`.
3. Add `sqlc.yaml` with `engine: "sqlite"` and Go output under `internal/db`.
4. Generate code with `sqlc generate`.
5. Keep `Cache` as the public adapter that converts generated rows to the CLI's
   `Repo`, `RunRecord`, and `JobRecord` types.
6. Add a `make sqlc` or `make generate` check so generated code cannot drift.
