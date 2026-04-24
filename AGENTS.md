# Agent Notes

This repo contains `gh-actions-usage`, a GitHub CLI extension for cached GitHub
Actions usage analysis. Keep this file short and update it only when a durable
repo convention changes.

## Working Rules

- Address Prateek as `Prateek` in user-facing messages.
- Use small, direct changes. Do not rewrite the storage layer or web UI without
  explicit direction.
- Run `make check` before committing. It runs unit tests, integration tests, the
  fixture-backed e2e script, `go build`, and `git diff --check`.
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

## CLI UX

- `report` is the primary refresh-plus-summary command.
- `serve --refresh` is the primary refresh-plus-dashboard command.
- Cached inspection commands (`summary`, `runs list`, `jobs list`, `serve`
  without `--refresh`) must not hit GitHub.
- Manual ingestion is only an escape hatch under `doctor ingest actions`.
- Actions reports carry account and billing-owner attribution, but GitHub
  billing usage rows are the invoice source of truth. Keep the two data sources
  separate in commands, storage, docs, and tests.

## Storage

- The current storage code uses `database/sql` directly against SQLite.
- `sqlc` is a good future fit if the schema grows or query churn increases. See
  `docs/sqlc-evaluation.md` before introducing generated database code.
