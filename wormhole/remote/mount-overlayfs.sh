#!/bin/sh
# goal: produce a /nix directory that has everything from rootfs ro and the three specific directories (orb/data, store, var) as rw
echo mounting overlayfs

mkdir -p /data/upper /data/work

ROOTFS=/wormhole-rootfs
# /data is attached via docker volume
UPPER=/data/upper
WORK=/data/work
mount -t overlay overlay -o lowerdir=$ROOTFS,upperdir=$UPPER,workdir=$WORK /mnt/wormhole-overlay

echo mounted wormhole-overlay


# make all rootfs ro except for some specific directories
mount --bind -o ro $ROOTFS /mnt/wormhole-unified

mount --bind /mnt/wormhole-overlay/nix/store /mnt/wormhole-unified/nix/store
mount --bind /mnt/wormhole-overlay/nix/var /mnt/wormhole-unified/nix/var
mount --bind /mnt/wormhole-overlay/nix/orb/data /mnt/wormhole-unified/nix/orb/data

echo finished wormhole-unified mount bind 

# copy over the write-files to wormhole-unified


./wormhole-server