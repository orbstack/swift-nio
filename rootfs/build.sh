#!/usr/bin/env bash

set -eo pipefail

# require root
if [ "$(id -u)" != "0" ]; then
    echo "This script must be run as root" 1>&2
    exit 1
fi

HOME=/home/dragon

cd "$(dirname "$0")"
rm -fr rd
mkdir rd
pushd rd

# ALPINE
tar xvf $HOME/Downloads/alpine-minirootfs-20220809-aarch64.tar.gz 
cp ../build-inside.sh .
# for custom lxd builds
cp ../packages/*.pub etc/apk/keys/
cp -r ../packages .
arch-chroot . /bin/sh -l /build-inside.sh

rm build-inside.sh
rm -r packages

# init and other scripts
OPT=opt/vc
cp -r ../vc $OPT
# legal
cp ../LICENSE .

# preinit
cp ../rd-compile/switch_overlay_root $OPT
# vcontrol server
cp ../../vcontrol/target/aarch64-unknown-linux-musl/release/vcontrol $OPT
# debugging tools
cp ../rd-compile/eventstat/eventstat $OPT


# TODO handle ssh host keys
cp ../../vcontainer86/rd/data/etc/ssh/ssh_host_* ./data/etc/ssh/

# data volume
popd
rm -fr data
mkdir data
mv rd/data/* data

# lxd preseed (not for void)
mkdir -p data/var/lib/lxd
mkdir -p data/var/cache/lxd
mkdir -p data/var/log/lxd
cp -raf lxd-preseed/var/lib/lxd/. data/var/lib/lxd/
chown -R root:root data/var/lib/lxd

./pack-disk.sh
