# Agent Notes

This repo contains `gh-actions-usage`, a GitHub CLI extension for cached GitHub
Actions usage analysis. Keep this file short and update it only when a durable
repo convention changes.

## Working Rules

- Address Prateek as `Prateek` in user-facing messages.
- Use small, direct changes. Do not rewrite the storage layer or web UI without
  explicit direction.
- Run `make check` before committing. It runs Go tests, the fixture-backed e2e
  script, the 95% coverage gate, a temporary `go build`, and `git diff --check`.
- Use `make fmt` to rewrite formatting. `make check` must stay non-mutating.
- Do not use `git commit --no-verify` or any hook-bypass flag.

## Cache Paths

- The default cache path follows XDG:
  `${XDG_CACHE_HOME:-$HOME/.cache}/gh-actions-usage/cache.db`.
- `GH_ACTIONS_USAGE_CACHE` is the explicit file-path override.
- Ignore relative `XDG_CACHE_HOME` values; the XDG spec requires absolute paths.
- Cache parent directories are created with `0700` permissions.

## Demos And Fixtures

- `testdata/demo-export.json` is the offline fixture for docs and e2e tests.
- `docs/showboat-demo.md` is a Showboat/Rodney transcript. If CLI output or UI
  behavior changes, regenerate or verify it with:

```bash
uvx showboat verify docs/showboat-demo.md
```

- Prefer `make docs-check` and `make docs-update` for Showboat/Rodney docs so
  the transcript and dashboard screenshot are handled the same way locally and
  in review.
- Showboat demos must print the evidence they rely on. Do not hide Rodney,
  server, API, screenshot, or file-type output only in `/tmp` files.

## CLI UX

- Help and cached read commands must be side-effect-free. A `--help` flag must
  never initialize the cache, refresh, import, clear, or write cache data.
- Destructive commands need explicit tests proving help and malformed args do
  not mutate state.
- `report` is the primary refresh-plus-summary command.
- `serve --refresh` is the primary refresh-plus-dashboard command.
- Cached inspection commands (`summary`, `runs list`, `jobs list`, `serve`
  without `--refresh`) must not hit GitHub.
- Manual ingestion is only an escape hatch under `doctor ingest actions`.
- JSON output shape is part of the CLI contract. When it changes, update tests,
  README examples, fixtures, and Showboat output together.
- Actions reports carry account and billing-owner attribution, but GitHub
  billing usage rows are the invoice source of truth. Keep the two data sources
  separate in commands, storage, docs, and tests.
- Monthly-limit investigations must check budgets, spending limits, and other
  hard stops. Billing usage rows explain spend; they do not explain every block.

## Storage

- The cache schema and stable queries live in `internal/db/*.sql` and generate
  typed Go with `sqlc`.
- Run `make sqlc` after editing `internal/db/schema.sql` or
  `internal/db/query.sql`; `make check` runs the non-mutating `make sqlc-check`.
- Keep `Cache` as the public adapter around generated `internal/db` row types.
- When adding fields to exported or cached structs, add an export/import
  round-trip test. This matters most for types with custom `UnmarshalJSON`.
- Multi-row cache writes should use transactions when partial state would be
  misleading.

## Release

- Releases are created by `make release VERSION=vX.Y.Z`, which runs checks,
  creates an annotated tag, and pushes it to trigger `.github/workflows/release.yml`.
- Use `make release-status VERSION=vX.Y.Z` to inspect the workflow and release.
- Release workflows must run `make check`; local release preflight alone is not
  enough.
- Before judging installed behavior, check the actual `gh` extension checkout.
  Local extension clones under `gh` can lag behind this repo.
- Source-install wrapper changes must account for all Go files, `go.mod`,
  `go.sum`, and embedded web assets.

## GitHub APIs

- Verify endpoint parameters against current official GitHub REST docs before
  adding flags. Billing `/usage` and `/usage/summary` support different filters.
- Actions run searches can be capped by GitHub. If refresh logic changes, test
  pagination and truncation behavior explicitly.
