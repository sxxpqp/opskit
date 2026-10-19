#!/usr/bin/env bash
# Explicit opt-in: privileged containers execute real host preparation, runtime
# installation, kubeadm init and CNI changes. Never run on a production daemon.
set -Eeuo pipefail

if [[ "${OPSKIT_ALLOW_PRIVILEGED_INSTALL_E2E:-}" != "1" ]]; then
  echo "Set OPSKIT_ALLOW_PRIVILEGED_INSTALL_E2E=1 on a disposable Linux Docker host to run this destructive test." >&2
  exit 2
fi
if [[ "$(uname -s)" != "Linux" ]]; then
  echo "Real kubeadm install E2E is currently Linux-only; Docker Desktop is not validated." >&2
  exit 2
fi

# Docker cannot reliably change kernel-wide swap state. Only a dedicated host
# already configured without active swap is eligible for this true install test.
if [[ "$(wc -l < /proc/swaps)" -ne 1 ]]; then
  echo "Host swap is active. Disable it on a disposable test host before starting; refusing to alter host swap from Docker." >&2
  exit 2
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$root"
report_dir="$root/tests/e2e/docker/artifacts"
mkdir -p "$report_dir/full-install"
export OPSKIT_E2E_REPORT_DIR="$report_dir"

project="opskit-install-${GITHUB_RUN_ID:-local}-$$"
compose=(docker compose -f tests/e2e/docker/compose.yaml -f tests/e2e/docker/full-install.compose.yaml -p "$project")

cleanup() {
  status=$?
  trap - EXIT
  # Collect non-secret diagnostics before containers are removed.
  "${compose[@]}" ps > "$report_dir/full-install/compose-ps.log" 2>&1 || true
  "${compose[@]}" logs --no-color > "$report_dir/full-install/compose.log" 2>&1 || true
  # Run in-container diagnostic script over stdin. No Docker daemon socket or
  # kubeconfig is uploaded; only sanitized operational logs leave the container.
  "${compose[@]}" exec -T master bash -s < tests/e2e/docker/collect-install-diagnostics.sh \
    > "$report_dir/full-install/node-diagnostics.log" 2>&1 || true
  "${compose[@]}" down --volumes --remove-orphans || true
  exit "$status"
}
trap cleanup EXIT

echo "Launching disposable privileged systemd node (real kubeadm installation)..."
"${compose[@]}" up --build -d --wait master
# Fail quickly if nested containerd would store image snapshots on overlay2.
storage_type="$("${compose[@]}" exec -T master findmnt -T /var/lib/containerd -n -o FSTYPE)"
echo "Nested containerd storage filesystem: ${storage_type}"
if [[ "$storage_type" == overlay || "$storage_type" == overlayfs ]]; then
  echo "Nested containerd storage remains on Docker's overlay rootfs, refusing invalid install experiment." >&2
  exit 1
fi
"${compose[@]}" run --build --rm --no-deps tester
