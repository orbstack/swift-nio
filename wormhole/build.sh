#!/bin/sh

set -euxo pipefail

cd "$(dirname "$0")"
cd ..

VERSION="1.0.0"

rm -rf out/wormhole
mkdir -p out/wormhole

VERSION="$VERSION" PLATFORM="linux/amd64" ARCH="amd64" HOST_ARCH="amd64" docker buildx bake -f rootfs/docker-bake.hcl wormhole
VERSION="$VERSION" PLATFORM="linux/arm64" ARCH="arm64" HOST_ARCH="arm64" docker buildx bake -f rootfs/docker-bake.hcl wormhole

# todo: swap version order
docker push registry.orb.local/wormhole:${VERSION}-amd64
docker push registry.orb.local/wormhole:${VERSION}-arm64

docker manifest rm registry.orb.local/wormhole:${VERSION} || :
docker manifest create registry.orb.local/wormhole:${VERSION} \
    registry.orb.local/wormhole:${VERSION}-amd64 \
    registry.orb.local/wormhole:${VERSION}-arm64
docker manifest push registry.orb.local/wormhole:${VERSION}

