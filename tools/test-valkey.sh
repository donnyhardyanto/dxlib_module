#!/bin/sh
# Runs the tests that need a real Valkey (module/session, module/credentials, module/passkey, module/user_photo) against a throwaway Valkey container.
#
#   tools/test-valkey.sh [go test flags...]      e.g. tools/test-valkey.sh -v -run Valkey
#
# PODMAN overrides the container command (it may be several words). By default it is "agentq podman" on macOS,
# where containers run in agentq's Podman machine, and plain podman on Linux (a runner), where podman runs natively.
# The container is removed afterwards.
#
# Valkey listens twice: plain TCP (the admin connection and most tests) and TLS only, with client certificates
# required (the TLS tests). A one-day CA, a server certificate for the name valkey.test (no IP SAN, so the tests must
# set the server name) and a client certificate are made with openssl in a temporary folder and copied into the
# container with podman cp rather than bind-mounted, so the run does not depend on which host folders the podman
# machine shares.
set -eu

if [ -z "${PODMAN:-}" ]; then
  case $(uname -s) in
    Darwin) PODMAN="agentq podman" ;;
    *) PODMAN=podman ;;
  esac
fi
IMAGE=docker.io/valkey/valkey:9.1.3
NAME=dxlib-module-valkey-test-$$
PORT=${VALKEY_TEST_PORT:-16379}
TLS_PORT=${VALKEY_TEST_TLS_PORT:-16380}
ADMIN_PASSWORD=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')

cd "$(dirname "$0")/.."

TLS_DIR=$(mktemp -d "${TMPDIR:-/tmp}/dxlib-module-valkey-tls.XXXXXX")
trap 'rm -rf "$TLS_DIR"' EXIT INT TERM

# make_cert <name> <CN> <extensions>: a P-256 key and a certificate signed by the test CA.
make_cert() {
  printf '%s\n' "$3" > "$TLS_DIR/$1.ext"
  if ! { openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -keyout "$TLS_DIR/$1.key" -out "$TLS_DIR/$1.csr" \
      -subj "/CN=$2" &&
    openssl x509 -req -in "$TLS_DIR/$1.csr" -CA "$TLS_DIR/ca.crt" -CAkey "$TLS_DIR/ca.key" -CAcreateserial \
      -days 1 -sha256 -extfile "$TLS_DIR/$1.ext" -out "$TLS_DIR/$1.crt"; } 2>"$TLS_DIR/openssl.err"; then
    echo "test-valkey: making the $1 certificate with openssl failed:" >&2
    cat "$TLS_DIR/openssl.err" >&2
    exit 1
  fi
}
if ! openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -keyout "$TLS_DIR/ca.key" -out "$TLS_DIR/ca.crt" \
  -days 1 -sha256 -subj "/CN=dxlib-module test CA" \
  -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" 2>"$TLS_DIR/openssl.err"; then
  echo "test-valkey: making the test CA with openssl failed:" >&2
  cat "$TLS_DIR/openssl.err" >&2
  exit 1
fi
make_cert server valkey.test "subjectAltName=DNS:valkey.test
keyUsage=critical,digitalSignature
extendedKeyUsage=serverAuth"
make_cert client session-store "keyUsage=critical,digitalSignature
extendedKeyUsage=clientAuth"
chmod 755 "$TLS_DIR"
chmod 644 "$TLS_DIR"/*

$PODMAN create --rm --name "$NAME" -p "127.0.0.1:$PORT:6379" -p "127.0.0.1:$TLS_PORT:6380" "$IMAGE" \
  valkey-server --requirepass "$ADMIN_PASSWORD" --maxmemory-policy noeviction --save '' --appendonly no \
  --tls-port 6380 --tls-cert-file /tls/server.crt --tls-key-file /tls/server.key --tls-ca-cert-file /tls/ca.crt \
  --tls-auth-clients yes >/dev/null
trap '$PODMAN stop -t 2 "$NAME" >/dev/null 2>&1 || $PODMAN rm -f "$NAME" >/dev/null 2>&1 || true; rm -rf "$TLS_DIR"' EXIT INT TERM
$PODMAN cp "$TLS_DIR" "$NAME:/tls"
$PODMAN start "$NAME" >/dev/null

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
DXLIB_MODULE_TEST_VALKEY_TLS=127.0.0.1:$TLS_PORT DXLIB_MODULE_TEST_VALKEY_TLS_DIR=$TLS_DIR \
  go test -count=1 "$@" ./module/session/ ./module/credentials/ ./module/passkey/ ./module/user_photo/
