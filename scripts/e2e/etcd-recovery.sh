#!/usr/bin/env bash
# All etcd processes and restore directories are isolated from kubeadm's etcd.
set -euo pipefail
umask 077
: "${OPSKIT:?Set OPSKIT to the binary under validation}"
: "${ETCD_BIN_DIR:?Set ETCD_BIN_DIR to matching etcd/etcdctl/etcdutl binaries}"
work=$(mktemp -d)
original_pid=''
restored_pid=''
cleanup() {
  test -z "$original_pid" || kill "$original_pid" 2>/dev/null || true
  test -z "$restored_pid" || kill "$restored_pid" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT
export ETCDCTL_API=3
"$ETCD_BIN_DIR/etcd" --name original --data-dir "$work/original" \
  --listen-client-urls http://127.0.0.1:12379 --advertise-client-urls http://127.0.0.1:12379 \
  --listen-peer-urls http://127.0.0.1:12380 --initial-advertise-peer-urls http://127.0.0.1:12380 \
  --initial-cluster original=http://127.0.0.1:12380 > "$work/original.log" 2>&1 &
original_pid=$!
ctl() { "$ETCD_BIN_DIR/etcdctl" --endpoints=http://127.0.0.1:12379 "$@"; }
for attempt in $(seq 1 30); do if ctl endpoint health >/dev/null 2>&1; then break; fi; sleep 1; done
ctl put /opskit/recovery-test preserved-value
ctl put /opskit/second-key second-value
printf 'schema_version: 1\n' > "$work/config.yaml"
"$OPSKIT" etcd backup --config "$work/config.yaml" --etcdctl "$ETCD_BIN_DIR/etcdctl" --endpoint http://127.0.0.1:12379 --directory "$work/backups" > "$work/backup.json"
task=$(find "$work/backups" -mindepth 1 -maxdepth 1 -type d | head -n 1)
"$OPSKIT" backup verify --directory "$task" --config "$work/config.yaml"
"$ETCD_BIN_DIR/etcdutl" snapshot restore "$task/snapshot.db" --name restored --data-dir "$work/restored" \
  --initial-cluster restored=http://127.0.0.1:22380 --initial-advertise-peer-urls http://127.0.0.1:22380 > "$work/restore.log" 2>&1
kill "$original_pid"; wait "$original_pid" || true; original_pid=''
"$ETCD_BIN_DIR/etcd" --name restored --data-dir "$work/restored" \
  --listen-client-urls http://127.0.0.1:22379 --advertise-client-urls http://127.0.0.1:22379 \
  --listen-peer-urls http://127.0.0.1:22380 --initial-advertise-peer-urls http://127.0.0.1:22380 \
  --initial-cluster restored=http://127.0.0.1:22380 > "$work/restored.log" 2>&1 &
restored_pid=$!
for attempt in $(seq 1 30); do if "$ETCD_BIN_DIR/etcdctl" --endpoints=http://127.0.0.1:22379 endpoint health >/dev/null 2>&1; then break; fi; sleep 1; done
test "$("$ETCD_BIN_DIR/etcdctl" --endpoints=http://127.0.0.1:22379 get /opskit/recovery-test --print-value-only)" = preserved-value
test "$("$ETCD_BIN_DIR/etcdctl" --endpoints=http://127.0.0.1:22379 get /opskit/second-key --print-value-only)" = second-value
echo "ETCD ISOLATED BACKUP / MANIFEST VERIFICATION / RESTORE / DATA READBACK PASSED; logs: $work"
