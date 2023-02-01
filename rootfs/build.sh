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
elif [[ "$BTYPE" != "debug" ]]; then
    echo "Unknown build type: $BTYPE"
    exit 1
fi

if [[ "$ARCH" != "arm64" ]] && [[ "$ARCH" != "amd64" ]]; then
    echo "Unknown architecture: $ARCH"
    exit 1
fi

# require root
if [ "$(id -u)" != "0" ]; then
    echo "This script must be run as root" 1>&2
    exit 1
fi

HOME=/home/dragon

cd "$(dirname "$0")"

# build vcontrol
pushd vcontrol
if [[ "$ARCH" == "arm64" ]]; then
    cargo build --release --target aarch64-unknown-linux-musl
else
    cargo build --release --target x86_64-unknown-linux-musl
fi
popd

# build macctl
pushd ../macvmgr
if [[ "$ARCH" == "arm64" ]]; then
    export GOARCH=arm64
else
    export GOARCH=amd64
fi
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" ./cmd/macctl
popd

# build scon (requires cgo)
if [[ "$ARCH" == "arm64" ]]; then
    compile_rd=rd-compile
else
    compile_rd=rd-compile86
fi
# private-users to fix git ownership for -buildvcs stamping
host_uid=$(id -u dragon)
host_gid=$(id -g dragon)
chown -R $host_uid:$host_gid $compile_rd/out || :
systemd-nspawn \
    --link-journal=no \
    -D $compile_rd \
    --private-users=$host_uid:65536 \
    --bind-ro="$PWD/..:/macvirt-src" \
    /bin/sh -l -c "set -eux; mkdir -p /out && cd /macvirt-src/scon && ./build-release.sh /out"

rm -fr rd
mkdir rd

# Alpine rootfs
if [[ "$ARCH" == "arm64" ]]; then
    rootfs_tar=$HOME/Downloads/alpine-minirootfs-20221110-aarch64.tar.gz 
else
    rootfs_tar=$HOME/Downloads/alpine-minirootfs-20221110-x86_64.tar.gz
fi
tar -C rd --numeric-owner -xf $rootfs_tar
# again for docker rootfs
mkdir -p rd/opt/docker-rootfs
tar -C rd/opt/docker-rootfs --numeric-owner -xf $rootfs_tar

pushd rd
cp ../build-inside.sh .
cp ../build-inside-docker.sh opt/docker-rootfs/
systemd-nspawn --link-journal=no -D . /bin/sh -l -c "IS_RELEASE=$IS_RELEASE; source /build-inside.sh" && \
    systemd-nspawn --link-journal=no -D opt/docker-rootfs /bin/sh -l -c "IS_RELEASE=$IS_RELEASE; source /build-inside-docker.sh"


rm build-inside.sh
rm opt/docker-rootfs/build-inside-docker.sh

# init and other scripts
OPT=opt/vc
GUEST_OPT=opt/macvirt-guest
cp -r ../utils/vc $OPT
cp -r ../utils/guest/* $GUEST_OPT
# legal
cp ../../LICENSE .

# ARCH DEPENDENT
# preinit
cp ../$compile_rd/switch_overlay_root $OPT
# nfs vsock
cp ../$compile_rd/add-nfsd-vsock $OPT
# scon
cp ../$compile_rd/out/{scon,scon-agent} $OPT
# macctl
cp ../../macvmgr/macctl $GUEST_OPT/bin
if [[ "$ARCH" == "arm64" ]]; then
    # vcontrol server
    cp ../vcontrol/target/aarch64-unknown-linux-musl/release/vcontrol $OPT
else
    # vcontrol server
    cp ../vcontrol/target/x86_64-unknown-linux-musl/release/vcontrol $OPT
fi


# TODO generate
if ! $IS_RELEASE; then
    touch $OPT/is_debug

    cp ../config/ssh_host_keys/* etc/ssh/
    chmod -R 0600 etc/ssh/*key*
fi

# data volume
popd

rm -fr out/rd
mv rd out/

pushd out
rm -fr data
mkdir data
mv rd/data/* data

../pack-disk.sh "$@"
