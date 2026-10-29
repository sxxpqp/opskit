#!/usr/bin/env bash
# Run only against the dedicated master1/worker1 Vagrant cluster.
set -euo pipefail
: "${KUBECONFIG:?Set KUBECONFIG for the dedicated validation cluster}"
image=${OPSKIT_E2E_IMAGE:-docker.io/library/busybox:1.36}
test "$(kubectl get nodes -o name | sort | tr '\n' ' ')" = 'node/master1 node/worker1 '
namespace="opskit-e2e-$(date +%s)-$$"
kubectl create namespace "$namespace"
cleanup() {
  code=$?
  if test "$code" != 0; then
    kubectl -n "$namespace" get pods -o wide || true
    kubectl -n "$namespace" get events --sort-by=.lastTimestamp | tail -n 20 || true
  fi
  kubectl delete namespace "$namespace" --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT
for node in master1 worker1; do
  kubectl -n "$namespace" apply -f - <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: server-$node
  labels: {app: echo-$node}
spec:
  nodeSelector: {kubernetes.io/hostname: $node}
  tolerations: [{operator: Exists}]
  containers:
    - name: server
      image: $image
      command: [sh, -c, 'mkdir -p /www; echo opskit-$node > /www/index.html; exec httpd -f -p 8080 -h /www']
      resources: {requests: {cpu: 10m, memory: 16Mi}}
---
apiVersion: v1
kind: Pod
metadata:
  name: client-$node
  labels: {role: client}
spec:
  nodeSelector: {kubernetes.io/hostname: $node}
  tolerations: [{operator: Exists}]
  containers:
    - name: client
      image: $image
      command: [sh, -c, 'sleep 3600']
      resources: {requests: {cpu: 10m, memory: 16Mi}}
YAML
done
kubectl -n "$namespace" wait --for=condition=Ready pods --all --timeout=180s
kubectl -n "$namespace" expose pod server-master1 --name echo --port=80 --target-port=8080
kubectl -n "$namespace" exec client-worker1 -- nslookup kubernetes.default.svc.cluster.local
kubectl -n "$namespace" exec client-worker1 -- nslookup "echo.$namespace.svc.cluster.local"
test "$(kubectl -n "$namespace" exec client-worker1 -- wget -qO- -T 5 "http://echo.$namespace.svc.cluster.local")" = opskit-master1
for pair in 'master1 worker1' 'worker1 master1'; do
  read -r source target <<< "$pair"
  address=$(kubectl -n "$namespace" get pod "server-$target" -o jsonpath='{.status.podIP}')
  test "$(kubectl -n "$namespace" exec "client-$source" -- wget -qO- -T 5 "http://$address:8080")" = "opskit-$target"
done
kubectl -n "$namespace" apply -f - <<'YAML'
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: deny-echo}
spec:
  podSelector: {matchLabels: {app: echo-master1}}
  policyTypes: [Ingress]
YAML
denied=false
for attempt in $(seq 1 15); do
  if ! kubectl -n "$namespace" exec client-worker1 -- wget -qO- -T 2 "http://echo.$namespace.svc.cluster.local" >/dev/null 2>&1; then denied=true; break; fi
  sleep 2
done
test "$denied" = true
kubectl -n "$namespace" apply -f - <<'YAML'
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: allow-client}
spec:
  podSelector: {matchLabels: {app: echo-master1}}
  policyTypes: [Ingress]
  ingress:
    - from: [{podSelector: {matchLabels: {role: client}}}]
      ports: [{protocol: TCP, port: 8080}]
YAML
allowed=false
for attempt in $(seq 1 15); do
  if result=$(kubectl -n "$namespace" exec client-worker1 -- wget -qO- -T 2 "http://echo.$namespace.svc.cluster.local" 2>/dev/null) && test "$result" = opskit-master1; then allowed=true; break; fi
  sleep 2
done
test "$allowed" = true
kubectl -n "$namespace" get pods -o wide
kubectl delete namespace "$namespace" --wait=true --timeout=120s
trap - EXIT
echo 'MULTINODE DNS / SERVICE / BIDIRECTIONAL POD NETWORK / NETWORK POLICY PASSED'
