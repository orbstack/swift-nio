#!/bin/sh

set -euxo pipefail

cd "$(dirname "$0")"
cd ..

VERSION="1"
BUCKET="orbstack-wormhole"

rm -rf out/wormhole
mkdir -p out/wormhole

HOST_ARCH="amd64"
if [[ "$(uname -m)" == "aarch64" ]] || [[ "$(uname -m)" == "arm64" ]]; then
    HOST_ARCH="arm64"
fi

VERSION="$VERSION" PLATFORM="linux/amd64" ARCH="amd64" HOST_ARCH="$HOST_ARCH" docker buildx bake -f rootfs/docker-bake.hcl wormhole
VERSION="$VERSION" PLATFORM="linux/arm64" ARCH="arm64" HOST_ARCH="$HOST_ARCH" docker buildx bake -f rootfs/docker-bake.hcl wormhole

docker push registry.orb.local/wormhole:amd64-${VERSION}
docker push registry.orb.local/wormhole:arm64-${VERSION}

docker manifest create --amend registry.orb.local/wormhole:${VERSION} \
    registry.orb.local/wormhole:amd64-${VERSION} \
    registry.orb.local/wormhole:arm64-${VERSION}
docker manifest push registry.orb.local/wormhole:${VERSION}

