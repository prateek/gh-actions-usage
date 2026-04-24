# gh-actions-usage

A GitHub CLI extension for cached GitHub Actions and billing usage analysis.

It loads workflow runs, jobs, and billing usage into a local SQLite cache, then
lets you query that cache repeatedly without burning GitHub API rate limit. The
embedded web UI includes an agentsview-style dashboard with a flamegraph,
duration histogram, filters, and slowest-job table.

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

Generate a fresh report for one repo:

```bash
gh actions-usage report \
  --account @me \
  --repo prateek/movies-do-app \
  --since 2026-04-01
```

Generate an account-attributed report across personal and org accounts:

```bash
gh actions-usage report \
  --account @me,my-org \
  --since 2026-04-01 \
  --account-plan prateek=pro,my-org=enterprise \
  --billing-owner my-org=my-enterprise \
  --billing-kind my-org=enterprise \
  --group-by account,billing-owner,billing-owner-kind,billing-plan,repo,runner-image
```

Refresh billing usage and summarize paid versus discounted rows:

```bash
gh actions-usage billing refresh --account @me,my-org,enterprise:my-enterprise --year 2026 --month 4
gh actions-usage billing summary --group-by account,product,sku,cost-class
```

Slice cached jobs without hitting GitHub:

```bash
gh actions-usage summary \
  --group-by date,repo,workflow-path,job,runner-image
```

Refresh and open the local dashboard:

```bash
gh actions-usage serve \
  --refresh \
  --account @me \
  --repo prateek/movies-do-app \
  --since 2026-04-01 \
  --open
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
gh actions-usage report --account @me|ORG[,ORG...] [--repo OWNER/NAME] [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--json]
gh actions-usage billing refresh --account @me|ORG|enterprise:SLUG[,...] [--year YYYY] [--month M] [--json]
gh actions-usage billing summary [--group-by account,product,sku,cost-class] [--json]
gh actions-usage summary [--group-by repo,workflow-path,job,runner-image] [--json]
gh actions-usage runs list [--repo OWNER/NAME] [--limit 50] [--json]
gh actions-usage jobs list [--repo OWNER/NAME] [--limit 50] [--json]
gh actions-usage export --out report.json
gh actions-usage import --in report.json [--json]
gh actions-usage serve [--refresh] [--account @me|ORG] [--repo OWNER/NAME] [--since YYYY-MM-DD] [--listen 127.0.0.1:8080] [--open]
gh actions-usage api get /user
gh actions-usage cache path|stats|clear
```

## Cache Model

The cache is SQLite and defaults to:

```text
${XDG_CACHE_HOME:-$HOME/.cache}/gh-actions-usage/cache.db
```

Override it with:

```bash
export GH_ACTIONS_USAGE_CACHE=/tmp/actions-usage.db
```

Relative `XDG_CACHE_HOME` values are ignored. The XDG spec requires absolute
base-directory paths.

Repeated refreshes are idempotent. Repositories, runs, and jobs are upserted
by stable GitHub IDs. Raw GitHub JSON is retained in the cache alongside parsed
fields so future versions can add new views without refetching old data.
Billing usage rows are upserted by a deterministic account/product/repo/date
key because GitHub billing usage items do not expose stable item IDs.

`report` and `serve --refresh` update the cache before reading it. `summary`,
`runs list`, `jobs list`, and `serve` without `--refresh` read only cached data.
For manual refresh debugging, use:

```bash
gh actions-usage doctor ingest actions \
  --account @me \
  --repo OWNER/REPO \
  --since YYYY-MM-DD
```

Billing refreshes call GitHub's billing usage endpoints for the account level
you request: user, organization, or enterprise. Those endpoints return usage
billed to that account, so account choice matters when Copilot or Actions usage
is billed through an org or enterprise. See GitHub's
[billing usage API docs](https://docs.github.com/en/rest/billing/usage) for the
current permissions and endpoint availability.

Useful Actions group dimensions:

```text
account, repo, repo-owner, repo-owner-kind, billing-owner,
billing-owner-kind, billing-plan, cost-class, date, workflow-path, job,
runner-type, runner-os, runner-arch, runner-image, platform, conclusion
```

Useful billing group dimensions:

```text
account, account-kind, date, year, month, product, sku, unit-type, model,
organization, repo, cost-center-id, cost-class
```

## JSON Policy

Use `--json` on read commands when scripting. JSON output goes to stdout.
Progress and errors go to stderr. Secret values are never printed.

Examples:

```bash
gh actions-usage jobs list --repo prateek/movies-do-app --limit 20 --json
gh actions-usage report --account @me --repo prateek/movies-do-app --since 2026-04-01 --json
gh actions-usage summary --group-by repo,runner-os,runner-image --json
gh actions-usage billing refresh --account @me,prateek-labs --year 2026 --month 4 --json
gh actions-usage billing summary --group-by account,product,sku,cost-class --json
gh actions-usage import --in actions-usage-report.json --json
gh actions-usage cache stats
```

## Web UI

`gh actions-usage serve` reads from the local cache. Add `--refresh` to update
the scoped repo/date range before the dashboard starts. It exposes:

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
make docs-check
```

Useful local smoke test:

```bash
GH_ACTIONS_USAGE_CACHE="$(mktemp -t gh-actions-usage).db" \
  go run . doctor --json
```

See [docs/demo.md](docs/demo.md) for a walkthrough.
See [docs/showboat-demo.md](docs/showboat-demo.md) for a captured Showboat/Rodney demo with command output and a dashboard screenshot.

## Demo Docs

The Showboat transcript is executable documentation. It also drives Rodney to
open the dashboard and capture `docs/assets/dashboard.png`.

```bash
make docs-check
make docs-update
```

`docs-check` fails if command output, browser assertions, or screenshot capture
break. `docs-update` rewrites the generated transcript, then verifies it again.

## Release

Releases are tag-driven. The GitHub Actions workflow at
`.github/workflows/release.yml` uses `cli/gh-extension-precompile` to publish
precompiled gh extension artifacts.

```bash
make release VERSION=v0.1.0
make release-status VERSION=v0.1.0
```

`make release` runs the full local check, requires a clean worktree, verifies
GitHub CLI auth, creates an annotated tag, and pushes it. The pushed tag starts
the release workflow.
