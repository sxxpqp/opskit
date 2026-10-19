#!/usr/bin/env bash
# Runs inside the disposable Kubernetes installation node.
# Collect only operational status, never admin.conf, tokens, secrets or full pod YAML.
set -uo pipefail

section() { printf '\n=== %s ===\n' "$1"; }

section "containerd mount and kernel"
findmnt -T /var/lib/containerd -o TARGET,SOURCE,FSTYPE,OPTIONS || true
uname -r
cat /proc/swaps || true

section "systemd status"
systemctl --no-pager --plain status kubelet containerd || true

section "Kubernetes node and system pod state"
if [[ -s /etc/kubernetes/admin.conf ]] && command -v kubectl >/dev/null 2>&1; then
  kube=(kubectl --kubeconfig /etc/kubernetes/admin.conf --request-timeout=8s)
  "${kube[@]}" get nodes -o wide || true
  "${kube[@]}" -n kube-system get pods -o wide || true
  "${kube[@]}" -n kube-system get daemonset calico-node -o wide || true
  "${kube[@]}" -n kube-system describe daemonset calico-node || true
  "${kube[@]}" -n kube-system get events --sort-by=.lastTimestamp | tail -n 75 || true
  for name in $("${kube[@]}" -n kube-system get pods -o name 2>/dev/null | grep 'calico-node-' || true); do
    "${kube[@]}" -n kube-system describe "$name" || true
  done
else
  echo "Kubernetes admin.conf or kubectl is not available yet"
fi

section "Container runtime pod status"
if command -v crictl >/dev/null 2>&1; then
  cri=(crictl --runtime-endpoint unix:///run/containerd/containerd.sock)
  "${cri[@]}" ps -a || true
  # Capture the actual reason Calico init failed; this was absent from earlier
  # logs, leaving a misleading generic cluster Ready timeout.
  for name in install-cni upgrade-ipam calico-node mount-bpffs; do
    section "Calico runtime logs: $name"
    ids=$("${cri[@]}" ps -a -q --name "$name" 2>/dev/null || true)
    if [[ -z "$ids" ]]; then
      echo "No container matched $name"
    fi
    while read -r id; do
      [[ -n "$id" ]] || continue
      printf 'Container: %s\n' "$id"
      "${cri[@]}" inspect "$id" 2>/dev/null | grep -E -m 6 '"(exitCode|reason|message|finishedAt)"' || true
      "${cri[@]}" logs --tail=120 "$id" 2>&1 || true
    done <<< "$ids"
  done
fi

section "Recent kubelet/containerd journal"
journalctl -u kubelet -u containerd --no-pager -n 100 || true
