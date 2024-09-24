#!/bin/sh
# goal: produce a /nix directory that has everything from rootfs ro and the three specific directories (orb/data, store, var) as rw
echo mounting overlayfs

ROOTFS=/wormhole-rootfs
UPPER=/data/upper
WORK=/data/work
OVERLAY=/mnt/wormhole-overlay
UNIFIED=/mnt/wormhole-unified


# make all rootfs ro except for some specific directories
mount ---bind $ROOTFS $UNIFIED
mount -o remount,bind,ro $ROOTFS $UNIFIED

mkdir -p $UPPER $WORK $OVERLAY $UNIFIED
mount -t overlay overlay -o lowerdir=$ROOTFS,upperdir=$UPPER,workdir=$WORK $OVERLAY
echo mounted overlay to $OVERLAY

# copy over the write-files to wormhole-unified
mount --bind $OVERLAY/nix/store $UNIFIED/nix/store
mount --bind $OVERLAY/nix/var $UNIFIED/nix/var
mount --bind $OVERLAY/nix/orb/data $UNIFIED/nix/orb/data

echo mounted folders from $OVERLAY to $UNIFIED
sleep infinite


