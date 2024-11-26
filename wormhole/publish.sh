#!/bin/sh

set -euxo pipefail

cd "$(dirname "$0")"
cd ..

ENVIRONMENT="$1"
BTYPE="$2"
if [ -z "$ENVIRONMENT" ] || [ -z "$BTYPE" ]; then
    echo "Usage: $0 <environment> <build type>"
    exit 1
fi

if [[ "$ENVIRONMENT" != "dev" ]] && [[ "$ENVIRONMENT" != "prod" ]]; then
    echo "Unknown environment: $ENVIRONMENT"
    exit 1
fi

if [[ "$BTYPE" != "release" ]] && [[ "$BTYPE" != "debug" ]]; then
    echo "Unknown build type: $BTYPE"
    exit 1
fi

BUCKET="orbstack-wormhole"
VERSION="$(head -n1 wormhole/version.txt)"

HOST_ARCH="amd64"
if [[ "$(uname -m)" == "aarch64" ]] || [[ "$(uname -m)" == "arm64" ]]; then
    HOST_ARCH="arm64"
fi

rm -rf out/wormhole
mkdir -p out/wormhole/amd64
mkdir -p out/wormhole/arm64

for ARCH in "amd64" "arm64"; do
    VERSION="$VERSION" BTYPE="$BTYPE" PLATFORM="linux/$ARCH" ARCH="$ARCH" HOST_ARCH="$HOST_ARCH" docker buildx bake -f rootfs/docker-bake.hcl wormhole
done

if [[ "$ENVIRONMENT" != "prod" ]]; then
    for ARCH in "amd64" "arm64"; do
        docker push registry.orb.local/wormhole:$VERSION-$ARCH
    done

    docker manifest rm registry.orb.local/wormhole:$VERSION || :
    docker manifest create registry.orb.local/wormhole:$VERSION \
        registry.orb.local/wormhole:$VERSION-amd64 \
        registry.orb.local/wormhole:$VERSION-arm64
    docker manifest push registry.orb.local/wormhole:$VERSION
else
    cd out/wormhole

    # export images
    for ARCH in "amd64" "arm64"; do
        docker save registry.orb.local/wormhole:$VERSION-$ARCH -o $ARCH/wormhole.tar
        tar -xf $ARCH/wormhole.tar -C $ARCH
    done

    # construct a multi-arch manifest list
    python3 ../../wormhole/scripts/make_manifest_index.py amd64/index.json arm64/index.json > index.json

    # upload platform-specific manifest image and blobs
    for ARCH in "amd64" "arm64"; do
        manifest_digest="$(jq -r '.manifests[0].digest | split(":")[1]' $ARCH/index.json)"
        aws s3 cp $ARCH/blobs/sha256/$manifest_digest s3://$BUCKET/manifests/sha256:$manifest_digest --metadata "{\"version\": \"$VERSION\"}" --content-type application/vnd.oci.image.manifest.v1+json

        for layer in $ARCH/blobs/sha256/*; do
            hash="${layer##*/}"
            aws s3 cp $layer s3://$BUCKET/blobs/sha256:$hash --metadata "{\"version\": \"$VERSION\"}" --content-type application/vnd.oci.image.layer.v1.tar
        done
    done

    # upload manifest list under both the tag and its sha256 digest
    index_digest="$(shasum -a 256 index.json | awk '{print $1}')"
    aws s3 cp index.json s3://$BUCKET/manifests/$VERSION --metadata "{\"version\": \"$VERSION\"}" --content-type application/vnd.oci.image.index.v1+json
    aws s3 cp index.json s3://$BUCKET/manifests/sha256:$index_digest --metadata "{\"version\": \"$VERSION\"}" --content-type application/vnd.oci.image.index.v1+json
fi
