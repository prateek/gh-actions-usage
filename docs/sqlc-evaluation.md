# sqlc Evaluation

Status: defer adoption for now.

## Context

The cache layer currently has one schema initializer, three upserts, three list
queries, stats, and clear. Tests cover idempotent writes, fixture import/export,
summary reads, and the HTTP handler.

`sqlc` is still a good fit for this repo later. It lets us write SQL once,
generate type-safe Go, and catch query/schema drift at generation time. Current
sqlc docs also support SQLite generation with `engine: "sqlite"`.

## Decision

Do not introduce sqlc in this patch. The storage layer is small, and the two
highest-value read paths build optional filters dynamically. A sqlc migration
would add generated code and adapters while some query composition would stay in
handwritten Go.

Revisit this when one of these becomes true:

- We add migrations or versioned schema changes.
- We add more aggregate queries for billing or runner analysis.
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
