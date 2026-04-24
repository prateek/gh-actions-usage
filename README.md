# gh-actions-usage

GitHub CLI extension for local GitHub Actions and billing usage analysis.

Sync Actions jobs and GitHub billing usage into SQLite. Slice by repo, account,
runner image, billing owner, and cost class. Includes a local dashboard with a
flamegraph and duration histogram.

## Install

```bash
gh extension install prateek/gh-actions-usage
```

From a checkout:

```bash
make install-local
gh actions-usage doctor
```

## Quick Start

```bash
# Check auth and cache
gh actions-usage doctor --json

# Find accounts and repos
gh actions-usage accounts list
gh actions-usage repos list --account @me

# Refresh Actions data and print a grouped report
gh actions-usage report \
  --account @me \
  --repo OWNER/REPO \
  --since 2026-04-01

# Query cached rows without hitting GitHub
gh actions-usage summary --group-by date,repo,workflow-path,runner-os,runner-image
```

Run `gh actions-usage --help` for the full command list. Most read commands
support `--json`; JSON goes to stdout and errors go to stderr.

## Billing Usage

GitHub bills usage at different account levels. Query the level that owns the
bill: `@me`, an org, or `enterprise:SLUG`.

```bash
gh actions-usage billing refresh \
  --account @me,my-org,enterprise:my-enterprise \
  --year 2026 \
  --month 4

gh actions-usage billing summary --group-by account,product,sku,cost-class
```

Actions reports can carry billing attribution when a repo's owner and payer are
different:

```bash
gh actions-usage report \
  --account @me,my-org \
  --since 2026-04-01 \
  --account-plan prateek=pro,my-org=enterprise \
  --billing-owner my-org=my-enterprise \
  --billing-kind my-org=enterprise \
  --group-by account,billing-owner,billing-owner-kind,billing-plan,repo,runner-image
```

## Dashboard

```bash
gh actions-usage serve \
  --refresh \
  --account @me \
  --repo OWNER/REPO \
  --since 2026-04-01 \
  --open
```

## Cache

Default cache path:

```text
${XDG_CACHE_HOME:-$HOME/.cache}/gh-actions-usage/cache.db
```

Override it with:

```bash
export GH_ACTIONS_USAGE_CACHE=/tmp/actions-usage.db
```

Refreshes are idempotent. Actions rows are upserted by GitHub IDs. Billing rows
use a deterministic account/product/repo/date key because GitHub's billing API
does not expose stable item IDs.

## Offline Demo

```bash
GH_ACTIONS_USAGE_CACHE=/tmp/gh-actions-usage-demo.db \
  gh actions-usage import --in testdata/demo-export.json

GH_ACTIONS_USAGE_CACHE=/tmp/gh-actions-usage-demo.db \
  gh actions-usage summary --group-by date,repo,workflow-path,runner-os,runner-image
```

See [docs/demo.md](docs/demo.md) for a walkthrough and
[docs/showboat-demo.md](docs/showboat-demo.md) for the Showboat/Rodney
transcript.

## Development

```bash
make test
make build
make install-local
make docs-check
```

Release:

```bash
make release VERSION=v0.1.0
make release-status VERSION=v0.1.0
```

Releases are tag-driven. `.github/workflows/release.yml` uses
`cli/gh-extension-precompile` to publish gh extension artifacts.
