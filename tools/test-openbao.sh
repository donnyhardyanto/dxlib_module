#!/bin/sh
# Runs the tests that need a real OpenBao (module/password's OpenBaoTransit) against a throwaway OpenBao dev server.
#
#   tools/test-openbao.sh [go test flags...]      e.g. tools/test-openbao.sh -v -run OpenBao
#
# PODMAN overrides the container command (default: podman). The container is removed afterwards. The dev server
# keeps everything in memory; the tests mount their own transit engines.
set -eu

PODMAN=${PODMAN:-podman}
IMAGE=docker.io/openbao/openbao:2.6.5
NAME=dxlib-module-openbao-test-$$
PORT=${OPENBAO_TEST_PORT:-18200}
ROOT_TOKEN=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')

cd "$(dirname "$0")/.."

$PODMAN run -d --rm --name "$NAME" -p "127.0.0.1:$PORT:8200" -e SKIP_SETCAP=true "$IMAGE" \
  server -dev -dev-root-token-id="$ROOT_TOKEN" -dev-listen-address=0.0.0.0:8200 >/dev/null
trap '$PODMAN stop -t 2 "$NAME" >/dev/null 2>&1 || true' EXIT INT TERM

i=0
until $PODMAN exec -e BAO_ADDR=http://127.0.0.1:8200 "$NAME" bao status >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -gt 100 ]; then
    echo "test-openbao: OpenBao container $NAME did not report itself unsealed (bao status) within 20 s" >&2
    $PODMAN logs "$NAME" >&2 || true
    exit 1
  fi
  sleep 0.2
done

DXLIB_MODULE_TEST_OPENBAO=http://127.0.0.1:$PORT DXLIB_MODULE_TEST_OPENBAO_ROOT_TOKEN=$ROOT_TOKEN \
  go test -count=1 "$@" ./module/password/
