#!/bin/sh
# Runs the tests that need a real PostgreSQL (module/passkey's DBStore) against a throwaway PostgreSQL container.
#
#   tools/test-postgres.sh [go test flags...]      e.g. tools/test-postgres.sh -v -run Postgres
#
# PODMAN overrides the container command (default: podman). The container is removed afterwards.
set -eu

PODMAN=${PODMAN:-podman}
IMAGE=docker.io/library/postgres:18.7
NAME=dxlib-module-postgres-test-$$
PORT=${POSTGRES_TEST_PORT:-15432}
ADMIN_PASSWORD=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')

cd "$(dirname "$0")/.."

$PODMAN run -d --rm --name "$NAME" -p "127.0.0.1:$PORT:5432" -e POSTGRES_PASSWORD="$ADMIN_PASSWORD" "$IMAGE" >/dev/null
trap '$PODMAN stop -t 2 "$NAME" >/dev/null 2>&1 || true' EXIT INT TERM

# pg_isready answers during the image's first-start initdb too, so wait for a query over TCP as the tests make it.
i=0
until $PODMAN exec -e PGPASSWORD="$ADMIN_PASSWORD" "$NAME" psql -h 127.0.0.1 -U postgres -tAc 'select 1' 2>/dev/null | grep -q 1; do
  i=$((i + 1))
  if [ "$i" -gt 150 ]; then
    echo "test-postgres: PostgreSQL container $NAME did not answer a query within 30 s" >&2
    $PODMAN logs "$NAME" >&2 || true
    exit 1
  fi
  sleep 0.2
done

DXLIB_MODULE_TEST_POSTGRES=127.0.0.1:$PORT DXLIB_MODULE_TEST_POSTGRES_ADMIN_PASSWORD=$ADMIN_PASSWORD \
  go test -count=1 "$@" ./module/passkey/
