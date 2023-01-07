#!/usr/bin/env bash

set -eo pipefail

# arm64, x86_64
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

if [[ "$ARCH" != "arm64" ]] && [[ "$ARCH" != "x86_64" ]]; then
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

# build vcontrol first
pushd vcontrol
if [[ "$ARCH" == "arm64" ]]; then
    cargo build --release --target aarch64-unknown-linux-musl
else
    cargo build --release --target x86_64-unknown-linux-musl
fi
popd

rm -fr rd
mkdir rd
pushd rd

# Alpine rootfs
if [[ "$ARCH" == "arm64" ]]; then
    tar xf $HOME/Downloads/alpine-minirootfs-20221110-aarch64.tar.gz 
else
    tar xf $HOME/Downloads/alpine-minirootfs-20221110-x86_64.tar.gz
fi

cp ../build-inside.sh .
# for custom lxd builds
cp ../packages/*.pub etc/apk/keys/
cp -r ../packages .
#arch-chroot . /bin/sh -l -c "IS_RELEASE=$IS_RELEASE; source /build-inside.sh"
systemd-nspawn -D . /bin/sh -l -c "IS_RELEASE=$IS_RELEASE; source /build-inside.sh"


rm build-inside.sh
rm -r packages

# init and other scripts
OPT=opt/vc
cp -r ../utils/vc $OPT
# legal
cp ../../LICENSE .

# ARCH DEPENDENT
if [[ "$ARCH" == "arm64" ]]; then
    # preinit
    cp ../rd-compile/switch_overlay_root $OPT
    # nfs vsock
    cp ../rd-compile/add-nfsd-vsock $OPT
    # vcontrol server
    cp ../vcontrol/target/aarch64-unknown-linux-musl/release/vcontrol $OPT
else
    # preinit
    cp ../rd-compile86/switch_overlay_root $OPT
    # nfs vsock
    cp ../rd-compile86/add-nfsd-vsock $OPT
    # vcontrol server
    cp ../vcontrol/target/x86_64-unknown-linux-musl/release/vcontrol $OPT
fi


# TODO generate
cp ../config/ssh_host_keys/* etc/ssh/
chmod -R 0600 etc/ssh/*key*

# data volume
popd

rm -fr out/rd
mv rd out/

pushd out
rm -fr data
mkdir data
mv rd/data/* data

# lxd preseed (not for void)
mkdir -p data/var/lib/lxd
mkdir -p data/var/cache/lxd
mkdir -p data/var/log/lxd
cp -raf ../lxd-preseed/var/lib/lxd/. data/var/lib/lxd/
chown -R root:root data/var/lib/lxd

../pack-disk.sh "$@"
