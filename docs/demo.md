# Demo Walkthrough

This demo is designed to be repeatable. `report` and `serve --refresh` update
the cache before reading it; rows are upserted by GitHub IDs.

For an executable transcript with captured command output and a browser
screenshot, see [showboat-demo.md](showboat-demo.md).

## 1. Check Setup

```bash
gh actions-usage doctor --json
```

Expected shape:

```json
{
  "extension": "gh-actions-usage",
  "auth": { "ok": true, "source": "gh", "login": "YOUR_LOGIN" },
  "cache": { "ok": true }
}
```

## 2. Find a Repository

```bash
gh actions-usage accounts list
gh actions-usage repos list --account @me
```

Pick a repo with recent Actions runs.

## 3. Generate a Fresh Report

```bash
gh actions-usage report \
  --account @me \
  --repo OWNER/REPO \
  --since 2026-04-01
```

For a personal plus organization view, pass multiple accounts and group by the
attribution fields:

```bash
gh actions-usage report \
  --account @me,ORG \
  --since 2026-04-01 \
  --account-plan YOUR_LOGIN=pro,ORG=enterprise \
  --billing-owner ORG=ENTERPRISE_SLUG \
  --billing-kind ORG=enterprise \
  --group-by account,billing-owner,billing-owner-kind,billing-plan,repo,runner-image
```

Run it again to prove idempotence:

```bash
gh actions-usage report \
  --account @me \
  --repo OWNER/REPO \
  --since 2026-04-01
```

Then inspect cache counts:

```bash
gh actions-usage cache stats
```

## 4. Slice the Cache

Top jobs:

```bash
gh actions-usage jobs list --repo OWNER/REPO --limit 20
```

Runner/image breakdown:

```bash
gh actions-usage summary \
  --repo OWNER/REPO \
  --group-by date,workflow-path,runner-os,runner-image
```

Machine-readable output:

```bash
gh actions-usage summary \
  --repo OWNER/REPO \
  --group-by repo,workflow-path,job,runner-image \
  --json
```

Billing usage comes from GitHub's billing API and is cached separately from job
telemetry:

```bash
gh actions-usage billing refresh \
  --account @me,ORG,enterprise:ENTERPRISE_SLUG \
  --year 2026 \
  --month 4

gh actions-usage billing summary \
  --group-by account,product,sku,cost-class
```

## 5. Open the Dashboard

```bash
gh actions-usage serve \
  --refresh \
  --account @me \
  --repo OWNER/REPO \
  --since 2026-04-01 \
  --open
```

Demo path through the UI:

1. Start with the runtime metric and failure rate.
2. Use the flamegraph to identify the largest repo/workflow/job/runner blocks.
3. Use the histogram to find long-tail jobs.
4. Filter by `macOS`, `ubuntu`, or a job name.
5. Use the slowest-job table to jump from aggregate cost to exact jobs.

Keyboard shortcuts:

- `/` focuses search.
- `t` toggles theme.

## 6. Export and Import

```bash
gh actions-usage export --out actions-usage-report.json
```

The export includes cached repositories, runs, and jobs. It does not refetch
data.

Import the same data into a fresh cache:

```bash
GH_ACTIONS_USAGE_CACHE=/tmp/gh-actions-usage-demo.db \
  gh actions-usage import --in actions-usage-report.json
```

The checked-in fixture at `testdata/demo-export.json` is safe for demos and e2e
tests because it never calls GitHub.

## 7. Manual Refresh Escape Hatch

For refresh debugging, use the doctor namespace:

```bash
gh actions-usage doctor ingest actions \
  --account @me \
  --repo OWNER/REPO \
  --since 2026-04-01
```
