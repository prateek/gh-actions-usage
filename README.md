# gh-actions-usage

A GitHub CLI extension for cached GitHub Actions usage analysis.

It ingests workflow runs and jobs into a local SQLite cache, then lets you query
that cache repeatedly without burning GitHub API rate limit. The embedded web UI
includes an agentsview-style dashboard with a flamegraph, duration histogram,
filters, and slowest-job table.

## Install

From GitHub:

```bash
gh extension install prateek/gh-actions-usage
```

From a local checkout:

```bash
make install-local
gh actions-usage doctor
```

The extension repository is named `gh-actions-usage`, so GitHub CLI exposes it
as:

```bash
gh actions-usage ...
```

## Quick Start

Check auth and cache:

```bash
gh actions-usage doctor --json
```

Discover accounts and repositories:

```bash
gh actions-usage accounts list
gh actions-usage repos list --account @me
```

Ingest one repo into the local cache:

```bash
gh actions-usage ingest actions \
  --account @me \
  --repo prateek/movies-do-app \
  --since 2026-04-01
```

Summarize cached jobs without hitting GitHub:

```bash
gh actions-usage summary \
  --group-by date,repo,workflow-path,job,runner-image
```

Open the local dashboard:

```bash
gh actions-usage serve --open
```

From a checkout, try the offline fixture without touching GitHub:

```bash
GH_ACTIONS_USAGE_CACHE=/tmp/gh-actions-usage-demo.db \
  gh actions-usage import --in testdata/demo-export.json

GH_ACTIONS_USAGE_CACHE=/tmp/gh-actions-usage-demo.db \
  gh actions-usage summary --group-by date,repo,workflow-path,runner-os,runner-image
```

## Commands

```text
gh actions-usage doctor [--json]
gh actions-usage accounts list [--json]
gh actions-usage repos list --account @me|ORG [--json]
gh actions-usage ingest actions --account @me|ORG [--repo OWNER/NAME] [--since YYYY-MM-DD] [--until YYYY-MM-DD]
gh actions-usage summary [--group-by repo,workflow-path,job,runner-image] [--json]
gh actions-usage runs list [--repo OWNER/NAME] [--limit 50] [--json]
gh actions-usage jobs list [--repo OWNER/NAME] [--limit 50] [--json]
gh actions-usage export --out report.json
gh actions-usage import --in report.json [--json]
gh actions-usage serve [--listen 127.0.0.1:8080] [--open]
gh actions-usage api get /user
gh actions-usage cache path|stats|clear
```

## Cache Model

The cache is SQLite and defaults to:

```text
~/Library/Caches/gh-actions-usage/cache.db
```

Override it with:

```bash
export GH_ACTIONS_USAGE_CACHE=/tmp/actions-usage.db
```

Repeated ingest runs are idempotent. Repositories, runs, and jobs are upserted
by stable GitHub IDs. Raw GitHub JSON is retained in the cache alongside parsed
fields so future versions can add new views without refetching old data.

## JSON Policy

Use `--json` on read commands when scripting. JSON output goes to stdout.
Progress and errors go to stderr. Secret values are never printed.

Examples:

```bash
gh actions-usage jobs list --repo prateek/movies-do-app --limit 20 --json
gh actions-usage summary --group-by repo,runner-os,runner-image --json
gh actions-usage import --in actions-usage-report.json --json
gh actions-usage cache stats
```

## Web UI

`gh actions-usage serve` reads only from the local cache. It exposes:

- `/` for the embedded dashboard.
- `/api/summary` for grouped summary JSON.
- `/api/jobs` for cached job rows.

The UI provides:

- Flamegraph: repo / workflow path / job / runner image.
- Histogram: job count by duration bucket.
- Filters: search, repo, runner OS, conclusion.
- Slowest jobs table.
- Keyboard shortcuts: `/` to search, `t` to toggle theme.

## Raw API Escape Hatch

Read-only raw API calls use the same auth path as the extension:

```bash
gh actions-usage api get /user
gh actions-usage api get /repos/prateek/movies-do-app/actions/runs
```

Raw writes are intentionally not implemented.

## Development

```bash
make test
make build
make install-local
```

Useful local smoke test:

```bash
GH_ACTIONS_USAGE_CACHE="$(mktemp -t gh-actions-usage).db" \
  go run . doctor --json
```

See [docs/demo.md](docs/demo.md) for a walkthrough.
See [docs/showboat-demo.md](docs/showboat-demo.md) for a captured Showboat/Rodney demo with command output and a dashboard screenshot.
