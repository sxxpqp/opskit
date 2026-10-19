#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ! -s /run/e2e/id_ed25519.pub || ! -s /run/e2e/password ]]; then
  echo "E2E SSH credentials are missing" >&2
  exit 1
fi

install -d -m 0700 -o opskit -g opskit /home/opskit/.ssh
install -m 0600 -o opskit -g opskit /run/e2e/id_ed25519.pub /home/opskit/.ssh/authorized_keys
password="$(cat /run/e2e/password)"
printf 'opskit:%s\n' "$password" | chpasswd
unset password

exec /sbin/init
