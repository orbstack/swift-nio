#!/usr/bin/env bash

set -eo pipefail

# arm64, amd64
ARCH="$1"
BTYPE="$2"
if [ -z "$ARCH" ] || [ -z "$BTYPE" ]; then
    echo "Usage: $0 <arch> <build type>"
    exit 1
fi

IS_RELEASE=false
if [[ "$BTYPE" == "release" ]]; then
    IS_RELEASE=true
elif [[ "$BTYPE" == "debug" ]]; then
    :
else
    echo "Unknown build type: $BTYPE"
    exit 1
fi

if [[ "$ARCH" != "arm64" ]] && [[ "$ARCH" != "amd64" ]]; then
    echo "Unknown architecture: $ARCH"
    exit 1
fi

cd "$(dirname "$0")"

# update killswitch - only in release, to avoid slow build
if $IS_RELEASE; then
    pushd ../scon
    go generate ./killswitch
    popd
fi

rm -fr out

platform="linux/amd64"
if [[ "$ARCH" == "arm64" ]]; then
    platform="linux/arm64"
fi

# detect host varch
HOST_ARCH="amd64"
if [[ "$(uname -m)" == "aarch64" ]] || [[ "$(uname -m)" == "arm64" ]]; then
    HOST_ARCH="arm64"
fi

# build packer and images
# TODO: migrate to buildx bake
docker build --build-arg TYPE=$BTYPE --build-arg ARCH=$ARCH --build-arg HOST_ARCH=$HOST_ARCH \
    --ssh "default=$SSH_AUTH_SOCK" \
    --platform "$platform" --load \
    -f Dockerfile --target images .. -t ghcr.io/orbstack/images:$BTYPE

# extract images
CID=$(docker create --platform "$platform" ghcr.io/orbstack/images:$BTYPE true)
trap "docker rm $CID" EXIT
docker cp -q $CID:/images out

# data and swap images
# can't be part of build due to privileged requirement for mounting images
docker run -i --rm --privileged --platform "$platform" -v $PWD/out:/out -v /dev:/hostdev ghcr.io/orbstack/images:$BTYPE < make-preseed.sh

copy_file() {
	mkdir -p ../assets/$BTYPE/$ARCH
    # delete and swap file to avoid overwrite
    # overwrite breaks running VM because rootfs.img changes behind its back
    rm -f ../assets/$BTYPE/$ARCH/$2
	cp "$1" ../assets/$BTYPE/$ARCH/$2
}

copy_file out/rootfs.img rootfs.img
copy_file out/data.img.tar data.img.tar
copy_file out/ri.cpio rpack
