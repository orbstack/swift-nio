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

# main build
docker build --build-arg TYPE=$BTYPE --platform "$platform" -f Dockerfile --target images .. -t orb/images:$BTYPE
# packer is always built for host arch
docker build --build-arg TYPE=$BTYPE -f Dockerfile --target packer .. -t orb/packer:$BTYPE

# extract images
CID=$(docker create --platform "$platform" orb/images:$BTYPE true)
trap "docker rm $CID" EXIT
docker cp -q $CID:/images out

# data and swap images
docker run -i --rm --privileged -v $PWD/out:/out orb/packer:$BTYPE < make-preseed.sh

copy_file() {
	mkdir -p ../assets/$BTYPE/$ARCH
	cp "$1" ../assets/$BTYPE/$ARCH/$2
}

copy_file out/rootfs.img rootfs.img
if [[ "$ARCH" == "arm64" ]]; then
	copy_file ~/code/android/kvm/linux/out/arch/arm64/boot/Image kernel
else
	copy_file ~/code/android/kvm/linux/out86/arch/x86/boot/bzImage kernel
fi
copy_file out/data.img.tar data.img.tar
copy_file out/swap.img.tar swap.img.tar
