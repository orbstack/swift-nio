#!/bin/sh

set -euxo pipefail

cd "$(dirname "$0")"
cd ..

BUCKET="orbstack-wormhole"
VERSION="$(head -n1 wormhole/version.txt)"

rm -rf out/wormhole
mkdir -p out/wormhole/amd64
mkdir -p out/wormhole/arm64

VERSION="$VERSION" BTYPE="release" PLATFORM="linux/amd64" ARCH="amd64" HOST_ARCH="amd64" docker buildx bake -f rootfs/docker-bake.hcl wormhole
VERSION="$VERSION" BTYPE="release" PLATFORM="linux/arm64" ARCH="arm64" HOST_ARCH="arm64" docker buildx bake -f rootfs/docker-bake.hcl wormhole
docker save registry.orb.local/wormhole:$VERSION-amd64 -o out/wormhole/amd64/wormhole.tar
docker save registry.orb.local/wormhole:$VERSION-arm64 -o out/wormhole/arm64/wormhole.tar

cd out/wormhole
tar -xf amd64/wormhole.tar -C amd64
tar -xf arm64/wormhole.tar -C arm64

# construct a multi-arch manifest list
python3 ../../wormhole/scripts/make_manifest_index.py amd64/index.json arm64/index.json > index.json

# the manifest list may also be directly referenced by its sha256 digest
index_digest="$(shasum -a 256 index.json | awk '{print $1}')"
amd64_manifest_digest="$(jq -r '.manifests[0].digest | split(":")[1]' amd64/index.json)"
arm64_manifest_digest="$(jq -r '.manifests[0].digest | split(":")[1]' arm64/index.json)"

# upload platform-specific blobs
for layer in "amd64/blobs/sha256"/*; do
    hash="${layer##*/}"
    aws s3 cp $layer s3://$BUCKET/blobs/sha256:$hash --metadata "{\"version\": \"$VERSION\"}" --content-type application/vnd.oci.image.layer.v1.tar
done

for layer in "arm64/blobs/sha256"/*; do
    hash="${layer##*/}"
    aws s3 cp $layer s3://$BUCKET/blobs/sha256:$hash --metadata "{\"version\": \"$VERSION\"}" --content-type application/vnd.oci.image.layer.v1.tar
done

# upload platform-specific manifest images
aws s3 cp amd64/blobs/sha256/$amd64_manifest_digest s3://$BUCKET/manifests/sha256:$amd64_manifest_digest --metadata "{\"version\": \"$VERSION\"}" --content-type application/vnd.oci.image.manifest.v1+json
aws s3 cp arm64/blobs/sha256/$arm64_manifest_digest s3://$BUCKET/manifests/sha256:$arm64_manifest_digest --metadata "{\"version\": \"$VERSION\"}" --content-type application/vnd.oci.image.manifest.v1+json

# upload manifest list under both the tag and the sha256 digest
aws s3 cp index.json s3://$BUCKET/manifests/$VERSION --metadata "{\"version\": \"$VERSION\"}" --content-type application/vnd.oci.image.index.v1+json
aws s3 cp index.json s3://$BUCKET/manifests/sha256:$index_digest --metadata "{\"version\": \"$VERSION\"}" --content-type application/vnd.oci.image.index.v1+json
