#!/usr/bin/env bash

set -eo pipefail

# arm64, amd64
BTYPE="$1"
if [ -z "$BTYPE" ]; then
    echo "Usage: $0 <build type>"
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

cd "$(dirname "$0")"

# update killswitch - only in release, to avoid slow build
if $IS_RELEASE; then
    pushd ../scon
    go generate ./killswitch
    popd
fi

rm -fr out

# detect host varch
HOST_ARCH="amd64"
if [[ "$(uname -m)" == "aarch64" ]] || [[ "$(uname -m)" == "arm64" ]]; then
    HOST_ARCH="arm64"
fi

# build packer and images
docker build --build-arg TYPE=$BTYPE --build-arg ARCH=$ARCH --build-arg HOST_ARCH=$HOST_ARCH \
    --platform linux/arm64,linux/amd64 \
    -f Dockerfile --target images .. -t orb/images:$BTYPE

for ARCH in arm64 amd64; do
    platform="linux/$ARCH"

    # extract images
    CID=$(docker create --platform "$platform" orb/images:$BTYPE true)
    trap "docker rm $CID" EXIT
    docker cp -q $CID:/images out

    # data and swap images
    docker run -i --rm --privileged -v $PWD/out:/out orb/images:$BTYPE < make-preseed.sh

    copy_file() {
        mkdir -p ../assets/$BTYPE/$ARCH
        cp "$1" ../assets/$BTYPE/$ARCH/$2
    }

    copy_file out/rootfs.img rootfs.img
    copy_file out/data.img.tar data.img.tar
    copy_file out/swap.img.tar swap.img.tar
done
