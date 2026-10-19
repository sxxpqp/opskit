#!/usr/bin/env bash
set -Eeuo pipefail

if [[ "${OPSKIT_FULL_INSTALL_E2E:-}" != "1" ]]; then
  echo "Refusing unexpected real-install entrypoint" >&2
  exit 2
fi

umask 077
mkdir -p /artifacts/full-install
report=/artifacts/full-install
cfg=/tmp/opskit-real-install.yaml
# The test process runs as container root and creates mode 0600 logs. Grant
# the disposable GitHub-hosted runner read access to *reports only*; the SSH
# key, kubeconfig, YAML and kubeadm bootstrap files remain private.
make_reports_readable() {
  find "$report" -maxdepth 1 -type f \( -name '*.log' -o -name 'result.txt' \) -exec chmod 0644 {} +
}
trap make_reports_readable EXIT

if [[ ! -s /run/e2e/password || ! -s /run/e2e/id_ed25519 ]]; then
  echo "Ephemeral SSH credentials not initialized" >&2
  exit 1
fi
export OPSKIT_DOCKER_E2E_PASSWORD
OPSKIT_DOCKER_E2E_PASSWORD="$(cat /run/e2e/password)"

/usr/local/bin/opskit-e2e-generate-config   /etc/opskit/config.example.yaml "$cfg" | tee "$report/config-summary.log"

echo "Running REAL OpsKit k8s create --dry-run..."
/usr/local/bin/opskit k8s create --config "$cfg" --dry-run > "$report/dry-run.log" 2>&1

# A bounded experiment should reveal a failed CNI early enough to preserve
# useful diagnostic logs; do not turn an API/Calico failure into a pass.
install_timeout="${OPSKIT_INSTALL_E2E_TIMEOUT:-15m}"
if [[ ! "$install_timeout" =~ ^[1-9][0-9]*m$ ]]; then
  echo "OPSKIT_INSTALL_E2E_TIMEOUT must be a positive integer number of minutes" >&2
  exit 2
fi
echo "Running REAL OpsKit k8s create (downloads, runtime, kubeadm init, Calico; timeout=$install_timeout)..."
echo "Streaming sanitized Kubernetes installation output to CI and $report/install.log."
# The redactor runs BEFORE tee. No unfiltered installer output is written to
# uploaded artifacts or the public Actions log (kubeadm prints join tokens).
# tee preserves a copy while Python flushes each sanitized line immediately.
set +e
/usr/local/bin/opskit k8s create --config "$cfg" --timeout "$install_timeout" 2>&1 \
  | python3 /usr/local/bin/opskit-e2e-stream-log \
  | tee "$report/install.log"
pipeline_status=("${PIPESTATUS[@]}")
set -e
status="${pipeline_status[0]}"
# The installer cannot be reported successful if the log sanitizer or tee
# failed. A non-zero installer exit status is still propagated unchanged.
if [[ "${pipeline_status[1]}" -ne 0 || "${pipeline_status[2]}" -ne 0 ]]; then
  echo "FAILED: real-time diagnostic log pipeline (redactor or tee)" | tee "$report/result.txt"
  exit 1
fi

if [[ "$status" -ne 0 ]]; then
  printf 'FAILED: opskit k8s create exit code %s\n' "$status" | tee "$report/result.txt"
  exit "$status"
fi

kubeconfig=/tmp/opskit-real-install-output/kubeconfig
if [[ ! -s "$kubeconfig" ]]; then
  echo "Installer returned success but no kubeconfig exists" | tee "$report/result.txt"
  exit 1
fi

# Download and SHA-256 verify the official kubectl client independently.
arch="$(dpkg --print-architecture)"
version=v1.28.15
base="https://dl.k8s.io/release/${version}/bin/linux/${arch}/kubectl"
curl --fail --location --silent --show-error --retry 3 "$base" -o /tmp/e2e-kubectl
curl --fail --location --silent --show-error --retry 3 "$base.sha256" -o /tmp/e2e-kubectl.sha256
printf '%s  %s\n' "$(tr -d ' \r\n' < /tmp/e2e-kubectl.sha256)" /tmp/e2e-kubectl | sha256sum --check
chmod 0755 /tmp/e2e-kubectl

echo "Verifying actual API /readyz, Node Ready, CoreDNS, and Calico..."
{
  /tmp/e2e-kubectl --kubeconfig "$kubeconfig" get --raw=/readyz
  /tmp/e2e-kubectl --kubeconfig "$kubeconfig" wait --for=condition=Ready node/master --timeout=5m
  /tmp/e2e-kubectl --kubeconfig "$kubeconfig" -n kube-system rollout status daemonset/calico-node --timeout=5m
  /tmp/e2e-kubectl --kubeconfig "$kubeconfig" -n kube-system rollout status deployment/coredns --timeout=5m
  /tmp/e2e-kubectl --kubeconfig "$kubeconfig" get nodes -o wide
  /tmp/e2e-kubectl --kubeconfig "$kubeconfig" get pods -A
} > "$report/verify.log" 2>&1 || {
  cat "$report/verify.log"
  echo "FAILED: Kubernetes post-install verification" | tee "$report/result.txt"
  exit 1
}

echo "PASS: OpsKit installed and verified Kubernetes on a new Linux systemd test container" | tee "$report/result.txt"
cat "$report/verify.log"
