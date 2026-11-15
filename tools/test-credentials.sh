#!/bin/sh
# Runs the tests of module/credentials that need real servers (the OpenBao provider, PostgresConnection, and
# RedisConnection with OpenBao KV credentials) against throwaway OpenBao, PostgreSQL and Valkey containers in one pod,
# so OpenBao's database secrets engine reaches PostgreSQL at 127.0.0.1:5432.
#
#   tools/test-credentials.sh [go test flags...]      e.g. tools/test-credentials.sh -v -run OpenBao
#
# By default it runs the OpenBao and PostgreSQL tests (a -run of your own replaces that); the package's other Valkey
# tests run under tools/test-valkey.sh (BUG-003).
#
# PODMAN overrides the container command (default: podman). The pod is removed afterwards. The OpenBao dev server
# keeps everything in memory; the tests mount their own KV and database engines.
set -eu

PODMAN=${PODMAN:-podman}
BAO_IMAGE=quay.io/openbao/openbao:2.7.2
PG_IMAGE=docker.io/library/postgres:18.7
VALKEY_IMAGE=docker.io/valkey/valkey:9.1.3
POD=dxlib-module-credentials-test-$$
BAO_PORT=${OPENBAO_TEST_PORT:-28200}
PG_PORT=${POSTGRES_TEST_PORT:-25432}
VALKEY_PORT=${VALKEY_TEST_PORT:-26379}
ROOT_TOKEN=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
PG_PASSWORD=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
VALKEY_PASSWORD=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')

cd "$(dirname "$0")/.."

$PODMAN pod create --name "$POD" -p "127.0.0.1:$BAO_PORT:8200" -p "127.0.0.1:$PG_PORT:5432" -p "127.0.0.1:$VALKEY_PORT:6379" >/dev/null
trap '$PODMAN pod rm -f -t 2 "$POD" >/dev/null 2>&1 || true' EXIT INT TERM

$PODMAN run -d --pod "$POD" --name "$POD-pg" -e POSTGRES_PASSWORD="$PG_PASSWORD" "$PG_IMAGE" >/dev/null
$PODMAN run -d --pod "$POD" --name "$POD-bao" -e SKIP_SETCAP=true "$BAO_IMAGE" \
  server -dev -dev-root-token-id="$ROOT_TOKEN" -dev-listen-address=0.0.0.0:8200 >/dev/null
$PODMAN run -d --pod "$POD" --name "$POD-valkey" "$VALKEY_IMAGE" \
  valkey-server --requirepass "$VALKEY_PASSWORD" --maxmemory-policy noeviction --save '' --appendonly no >/dev/null

# pg_isready answers during the image's first-start initdb too, so wait for a query over TCP as the tests make it.
i=0
until $PODMAN exec -e PGPASSWORD="$PG_PASSWORD" "$POD-pg" psql -h 127.0.0.1 -U postgres -tAc 'select 1' 2>/dev/null | grep -q 1; do
  i=$((i + 1))
  if [ "$i" -gt 150 ]; then
    echo "test-credentials: PostgreSQL container $POD-pg did not answer a query within 30 s" >&2
    $PODMAN logs "$POD-pg" >&2 || true
    exit 1
  fi
  sleep 0.2
done
i=0
until $PODMAN exec -e BAO_ADDR=http://127.0.0.1:8200 "$POD-bao" bao status >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -gt 100 ]; then
    echo "test-credentials: OpenBao container $POD-bao did not report itself unsealed (bao status) within 20 s" >&2
    $PODMAN logs "$POD-bao" >&2 || true
    exit 1
  fi
  sleep 0.2
done
i=0
until $PODMAN exec "$POD-valkey" valkey-cli -a "$VALKEY_PASSWORD" --no-auth-warning ping 2>/dev/null | grep -q PONG; do
  i=$((i + 1))
  if [ "$i" -gt 50 ]; then
    echo "test-credentials: Valkey container $POD-valkey did not answer PING within 10 s" >&2
    $PODMAN logs "$POD-valkey" >&2 || true
    exit 1
  fi
  sleep 0.2
done

DXLIB_MODULE_TEST_OPENBAO=http://127.0.0.1:$BAO_PORT DXLIB_MODULE_TEST_OPENBAO_ROOT_TOKEN=$ROOT_TOKEN \
DXLIB_MODULE_TEST_OPENBAO_POSTGRES=127.0.0.1:5432 \
DXLIB_MODULE_TEST_POSTGRES=127.0.0.1:$PG_PORT DXLIB_MODULE_TEST_POSTGRES_ADMIN_PASSWORD=$PG_PASSWORD \
DXLIB_MODULE_TEST_VALKEY=127.0.0.1:$VALKEY_PORT DXLIB_MODULE_TEST_VALKEY_ADMIN_PASSWORD=$VALKEY_PASSWORD \
  go test -count=1 -run '^Test(NewOpenBao|OpenBao|PostgresConnection|IsPostgresAuthError)' "$@" ./module/credentials/
