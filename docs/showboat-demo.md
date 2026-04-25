# gh-actions-usage: cached Actions analytics demo

*2026-04-24T23:38:40Z by Showboat 0.6.1*
<!-- showboat-id: 5de7e017-922d-47d3-b296-45823d7994f2 -->

This demo runs without calling GitHub. It imports the checked-in fixture, proves
that repeated imports are idempotent, slices the cache by runner image, and opens
the local dashboard with Rodney. Every command prints the evidence it depends on.

```bash
set -euo pipefail
export GH_ACTIONS_USAGE_CACHE=/tmp/gh-actions-usage-showboat-import.db
rm -f "$GH_ACTIONS_USAGE_CACHE" "$GH_ACTIONS_USAGE_CACHE-shm" "$GH_ACTIONS_USAGE_CACHE-wal"
printf "cache=%s\n" "$GH_ACTIONS_USAGE_CACHE"
go run . import --in testdata/demo-export.json --json | jq "{repos_imported,runs_imported,jobs_imported}"
go run . import --in testdata/demo-export.json --json | jq "{repos_imported,runs_imported,jobs_imported}"
go run . cache stats | jq .

```

```output
cache=/tmp/gh-actions-usage-showboat-import.db
{
  "repos_imported": 2,
  "runs_imported": 3,
  "jobs_imported": 7
}
{
  "repos_imported": 2,
  "runs_imported": 3,
  "jobs_imported": 7
}
{
  "billing_usage": 0,
  "jobs": 7,
  "repos": 2,
  "runs": 3
}
```

Both imports read the same seven jobs. The cache stores one row per GitHub ID, so
running the loader again updates rows instead of duplicating them.

```bash
set -euo pipefail
export GH_ACTIONS_USAGE_CACHE=/tmp/gh-actions-usage-showboat-summary.db
rm -f "$GH_ACTIONS_USAGE_CACHE" "$GH_ACTIONS_USAGE_CACHE-shm" "$GH_ACTIONS_USAGE_CACHE-wal"
go run . import --in testdata/demo-export.json >/dev/null
go run . summary --group-by date,repo,workflow-path,runner-os,runner-image

```

```output
jobs: 7
runs: 3
runtime: 56.5 minutes
date        repo             workflow-path               runner-os  runner-image   jobs  minutes  avg     longest
2026-04-24  demo/mobile-app  .github/workflows/ci.yml    macOS      macos-15       2     41.5     20m45s  24m30s
2026-04-24  demo/api         .github/workflows/test.yml  Linux      ubuntu-latest  2     7.5      3m45s   5m0s
2026-04-24  demo/mobile-app  .github/workflows/ci.yml    Linux      ubuntu-latest  3     7.5      2m30s   3m20s
```

The macOS jobs are the expensive part of this fixture: two jobs account for most
of the runtime. The next view shows the slowest individual jobs.

```bash
set -euo pipefail
export GH_ACTIONS_USAGE_CACHE=/tmp/gh-actions-usage-showboat-jobs.db
rm -f "$GH_ACTIONS_USAGE_CACHE" "$GH_ACTIONS_USAGE_CACHE-shm" "$GH_ACTIONS_USAGE_CACHE-wal"
go run . import --in testdata/demo-export.json >/dev/null
go run . jobs list --limit 4

```

```output
started     repo             workflow                    job         runner         result   duration
2026-04-24  demo/mobile-app  .github/workflows/ci.yml    ios tests   macos-15       failure  24m30s
2026-04-24  demo/mobile-app  .github/workflows/ci.yml    ios tests   macos-15       success  17m0s
2026-04-24  demo/api         .github/workflows/test.yml  api tests   ubuntu-latest  success  5m0s
2026-04-24  demo/mobile-app  .github/workflows/ci.yml    unit tests  ubuntu-latest  success  3m20s
```

The dashboard uses the same cache through `/api/summary` and `/api/jobs`.
Rodney starts Chrome, opens the dashboard, checks that the flamegraph and table
rendered, and captures a screenshot.

```bash
set -euo pipefail
export GH_ACTIONS_USAGE_CACHE=/tmp/gh-actions-usage-showboat-ui.db
export RODNEY_HOME=/tmp/gh-actions-usage-showboat-rodney
rm -rf "$RODNEY_HOME"
rm -f "$GH_ACTIONS_USAGE_CACHE" "$GH_ACTIONS_USAGE_CACHE-shm" "$GH_ACTIONS_USAGE_CACHE-wal"
server_pid=""
cleanup() {
  if [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null || true
  fi
  uvx rodney stop >/tmp/gh-actions-usage-rodney-stop.out 2>/dev/null || true
}
trap cleanup EXIT

go run . import --in testdata/demo-export.json >/dev/null
(go run . serve --listen 127.0.0.1:18184 >/tmp/gh-actions-usage-demo-server.out 2>/tmp/gh-actions-usage-demo-server.err & echo $! >/tmp/gh-actions-usage-demo-server.pid)
server_pid="$(cat /tmp/gh-actions-usage-demo-server.pid)"
ready=0
for _ in $(seq 1 40); do
  if curl -fsS http://127.0.0.1:18184/api/summary >/tmp/gh-actions-usage-demo-summary.json 2>/dev/null; then
    ready=1
    break
  fi
  sleep 0.25
done
if [ "$ready" -ne 1 ]; then
  cat /tmp/gh-actions-usage-demo-server.err >&2
  exit 1
fi
printf "server: serving http://127.0.0.1:18184\n"
printf "api summary:\n"
jq '{total_jobs,total_minutes,top_groups: [.groups[] | {repo: .values.repo, job: .values.job, runner_image: .values["runner-image"], minutes: .total_minutes}]}' /tmp/gh-actions-usage-demo-summary.json
uvx rodney start >/tmp/gh-actions-usage-rodney-start.out
printf "rodney start: "
sed -E 's/PID [0-9]+/PID <pid>/; s#ws://127\.0\.0\.1:[0-9]+/devtools/browser/[A-Za-z0-9-]+#ws://127.0.0.1:<port>/devtools/browser/<id>#' /tmp/gh-actions-usage-rodney-start.out
printf "rodney open: "
uvx rodney open http://127.0.0.1:18184/
printf "rodney wait: "
uvx rodney wait "#flamegraph"
printf "rodney title: "
uvx rodney title
printf "rodney assertions:\n"
uvx rodney assert "document.body.innerText.includes(\"Actions Usage\")" true
uvx rodney assert "document.querySelectorAll(\"#table tr\").length > 1" true
printf "rodney page data:\n"
uvx rodney js "({title: document.title, rows: document.querySelectorAll(\"#table tr\").length, flamegraph: Boolean(document.querySelector(\"#flamegraph\")), histogram: Boolean(document.querySelector(\"#histogram\"))})"
uvx rodney screenshot -w 1440 -h 1100 docs/assets/dashboard.png
file docs/assets/dashboard.png

```

```output
server: serving http://127.0.0.1:18184
api summary:
{
  "total_jobs": 7,
  "total_minutes": 56.5,
  "top_groups": [
    {
      "repo": "demo/mobile-app",
      "job": "ios tests",
      "runner_image": "macos-15",
      "minutes": 41.5
    },
    {
      "repo": "demo/mobile-app",
      "job": "unit tests",
      "runner_image": "ubuntu-latest",
      "minutes": 6.333333333333333
    },
    {
      "repo": "demo/api",
      "job": "api tests",
      "runner_image": "ubuntu-latest",
      "minutes": 5
    },
    {
      "repo": "demo/api",
      "job": "package",
      "runner_image": "ubuntu-latest",
      "minutes": 2.5
    },
    {
      "repo": "demo/mobile-app",
      "job": "lint",
      "runner_image": "ubuntu-latest",
      "minutes": 1.1666666666666667
    }
  ]
}
rodney start: Chrome started (PID <pid>)
Debug URL: ws://127.0.0.1:<port>/devtools/browser/<id>
rodney open: GH Actions Usage
rodney wait: Element visible
rodney title: GH Actions Usage
rodney assertions:
pass
pass
rodney page data:
{
  "flamegraph": true,
  "histogram": true,
  "rows": 7,
  "title": "GH Actions Usage"
}
docs/assets/dashboard.png
docs/assets/dashboard.png: PNG image data, 1440 x 1100, 8-bit/color RGB, non-interlaced
```

Showboat embeds the Rodney screenshot below. The image is copied into the docs
folder so the Markdown preview has the same visual evidence as the terminal
transcript.

```bash {image}
![Dashboard screenshot with flamegraph, histogram, summary metric cards, and slowest jobs table](docs/assets/dashboard.png)
```

![Dashboard screenshot with flamegraph, histogram, summary metric cards, and slowest jobs table](7325e2e6-2026-04-24.png)
