#!/usr/bin/env bash
set -Eeuo pipefail

install -d -m 0700 /artifacts
if [[ ! -s /run/e2e/password || ! -s /run/e2e/id_ed25519 ]]; then
  echo "E2E credentials not available to tester" >&2
  exit 1
fi

export OPSKIT_DOCKER_E2E=1

set +e
/usr/local/bin/opskit-app.test -test.v -test.run '^TestDockerK8s' \
  > /artifacts/ssh-systemd.log 2>&1
status=$?
set -e

cat /artifacts/ssh-systemd.log

if [[ "$status" -ne 0 ]]; then
  echo "Docker SSH/systemd integration checks failed (exit ${status})" >&2
fi
exit "$status"
