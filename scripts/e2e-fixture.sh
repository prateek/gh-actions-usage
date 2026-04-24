#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bin="${repo_root}/bin/gh-actions-usage"
cache="$(mktemp -t gh-actions-usage-e2e).db"
server_log="$(mktemp -t gh-actions-usage-server).out"
server_err="$(mktemp -t gh-actions-usage-server).err"
summary_json="$(mktemp -t gh-actions-usage-summary).json"
server_pid=""

cleanup() {
  if [[ -n "${server_pid}" ]]; then
    kill "${server_pid}" 2>/dev/null || true
  fi
  rm -f "${cache}" "${cache}-shm" "${cache}-wal" "${server_log}" "${server_err}" "${summary_json}"
}
trap cleanup EXIT

(cd "${repo_root}" && go build -o "${bin}" .)
GH_ACTIONS_USAGE_CACHE="${cache}" "${bin}" import --in "${repo_root}/testdata/demo-export.json" --json >/dev/null

summary="$(GH_ACTIONS_USAGE_CACHE="${cache}" "${bin}" summary --group-by date,repo,workflow-path,runner-os,runner-image)"
grep -q "runtime: 56.5 minutes" <<<"${summary}"
grep -q "macOS" <<<"${summary}"
grep -q "macos-15" <<<"${summary}"

GH_ACTIONS_USAGE_CACHE="${cache}" "${bin}" serve --listen 127.0.0.1:0 >"${server_log}" 2>"${server_err}" &
server_pid=$!

url=""
for _ in $(seq 1 40); do
  url="$(sed -n 's/^serving //p' "${server_log}" | head -n 1)"
  if [[ -n "${url}" ]] && curl -fsS "${url}/api/summary" >"${summary_json}"; then
    break
  fi
  sleep 0.25
done

if [[ ! -s "${summary_json}" ]]; then
  printf 'server did not become ready\n' >&2
  printf 'stdout:\n' >&2
  cat "${server_log}" >&2
  printf 'stderr:\n' >&2
  cat "${server_err}" >&2
  exit 1
fi

grep -Eq '"total_jobs":[[:space:]]*7' "${summary_json}"
grep -Eq '"total_minutes":[[:space:]]*56.5' "${summary_json}"
