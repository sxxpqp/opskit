#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$root"

report_dir="${OPSKIT_E2E_REPORT_DIR:-$root/tests/e2e/docker/artifacts}"
mkdir -p "$report_dir"
export OPSKIT_E2E_REPORT_DIR="$report_dir"

project="${OPSKIT_E2E_PROJECT:-opskit-e2e-${GITHUB_RUN_ID:-local}-$$}"
compose=(docker compose -f tests/e2e/docker/compose.yaml -p "$project")

cleanup() {
  status=$?
  trap - EXIT
  "${compose[@]}" logs --no-color > "$report_dir/compose.log" 2>&1 || true
  "${compose[@]}" down --volumes --remove-orphans || true
  exit "$status"
}
trap cleanup EXIT

# No host SSH ports, Docker socket mounts or production credentials.
"${compose[@]}" up -d --build --wait master worker
"${compose[@]}" run --build --rm tester
