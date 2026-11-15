#!/bin/sh
# Runs the tests that need a real S3 API (module/user_photo's S3Store) against a throwaway SeaweedFS container.
#
#   tools/test-seaweedfs.sh [go test flags...]      e.g. tools/test-seaweedfs.sh -v -run S3
#
# SeaweedFS's S3 gateway accepts every request when it has no identities, so the container gets two: an admin that
# creates the test bucket, and a photo identity allowed only Read, Write and List on that bucket, which the tests
# use through the credential provider. The anonymous identity may read only the bucket public-test, which a test
# makes to show that S3Store.Check refuses a bucket readable without a signature.
#
# PODMAN overrides the container command (default: podman). The container is removed afterwards.
set -eu

PODMAN=${PODMAN:-podman}
IMAGE=docker.io/chrislusf/seaweedfs:4.53
NAME=dxlib-module-seaweedfs-test-$$
PORT=${SEAWEEDFS_TEST_PORT:-18333}
BUCKET=user-photos-test
rand() { od -An -N16 -tx1 /dev/urandom | tr -d ' \n'; }
ADMIN_KEY=admin$(rand | cut -c1-12)
ADMIN_SECRET=$(rand)
PHOTO_KEY=photo$(rand | cut -c1-12)
PHOTO_SECRET=$(rand)
OTHER_KEY=other$(rand | cut -c1-12)
OTHER_SECRET=$(rand)

S3_CONFIG=$(cat <<EOF
{"identities":[
 {"name":"admin","credentials":[{"accessKey":"$ADMIN_KEY","secretKey":"$ADMIN_SECRET"}],"actions":["Admin","Read","List","Tagging","Write"]},
 {"name":"photos","credentials":[{"accessKey":"$PHOTO_KEY","secretKey":"$PHOTO_SECRET"}],"actions":["Read:$BUCKET","Write:$BUCKET","List:$BUCKET"]},
 {"name":"anonymous","actions":["Read:public-test","List:public-test"]},
 {"name":"other","credentials":[{"accessKey":"$OTHER_KEY","secretKey":"$OTHER_SECRET"}],"actions":["Read:another-bucket","Write:another-bucket","List:another-bucket"]}
]}
EOF
)

cd "$(dirname "$0")/.."

$PODMAN run -d --rm --name "$NAME" -p "127.0.0.1:$PORT:8333" -e S3_CONFIG="$S3_CONFIG" --entrypoint /bin/sh "$IMAGE" -c \
  'mkdir -p /tmp/data && printf "%s" "$S3_CONFIG" > /tmp/s3.json && exec weed server -dir=/tmp/data -ip=127.0.0.1 -ip.bind=0.0.0.0 -master.volumeSizeLimitMB=64 -volume.max=8 -s3 -s3.port=8333 -s3.config=/tmp/s3.json' >/dev/null
trap '$PODMAN stop -t 2 "$NAME" >/dev/null 2>&1 || true' EXIT INT TERM

# Ready once the S3 gateway answers HTTP at all; the tests check what it answers. Their admin client then creates
# the bucket and writes a first object, retrying while the volume server registers.
i=0
until [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/" 2>/dev/null)" != 000 ]; do
  i=$((i + 1))
  if [ "$i" -gt 300 ]; then
    echo "test-seaweedfs: SeaweedFS container $NAME did not answer on its S3 port within 60 s" >&2
    $PODMAN logs "$NAME" >&2 || true
    exit 1
  fi
  sleep 0.2
done

DXLIB_MODULE_TEST_S3=127.0.0.1:$PORT DXLIB_MODULE_TEST_S3_BUCKET=$BUCKET \
DXLIB_MODULE_TEST_S3_ADMIN_KEY=$ADMIN_KEY DXLIB_MODULE_TEST_S3_ADMIN_SECRET=$ADMIN_SECRET \
DXLIB_MODULE_TEST_S3_KEY=$PHOTO_KEY DXLIB_MODULE_TEST_S3_SECRET=$PHOTO_SECRET \
DXLIB_MODULE_TEST_S3_OTHER_KEY=$OTHER_KEY DXLIB_MODULE_TEST_S3_OTHER_SECRET=$OTHER_SECRET \
  go test -count=1 "$@" ./module/user_photo/
