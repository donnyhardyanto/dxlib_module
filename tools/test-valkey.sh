#!/bin/sh
# Runs the tests that need a real Valkey (module/session) against a throwaway Valkey container.
#
#   tools/test-valkey.sh [go test flags...]      e.g. tools/test-valkey.sh -v -run Valkey
#
# PODMAN overrides the container command (default: podman). The container is removed afterwards.
set -eu

PODMAN=${PODMAN:-podman}
IMAGE=docker.io/valkey/valkey:9.1.3
NAME=dxlib-module-valkey-test-$$
PORT=${VALKEY_TEST_PORT:-16379}
ADMIN_PASSWORD=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')

cd "$(dirname "$0")/.."

$PODMAN run -d --rm --name "$NAME" -p "127.0.0.1:$PORT:6379" "$IMAGE" \
  valkey-server --requirepass "$ADMIN_PASSWORD" --maxmemory-policy noeviction --save '' --appendonly no >/dev/null
trap '$PODMAN stop -t 2 "$NAME" >/dev/null 2>&1 || true' EXIT INT TERM

i=0
until $PODMAN exec "$NAME" valkey-cli -a "$ADMIN_PASSWORD" --no-auth-warning ping 2>/dev/null | grep -q PONG; do
  i=$((i + 1))
  if [ "$i" -gt 50 ]; then
    echo "test-valkey: Valkey container $NAME did not answer PING within 10 s" >&2
    $PODMAN logs "$NAME" >&2 || true
    exit 1
  fi
  sleep 0.2
done

DXLIB_MODULE_TEST_VALKEY=127.0.0.1:$PORT DXLIB_MODULE_TEST_VALKEY_ADMIN_PASSWORD=$ADMIN_PASSWORD \
  go test -count=1 "$@" ./module/session/ ./module/credentials/
