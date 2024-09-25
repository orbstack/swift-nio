#!/bin/sh
# goal: produce a /nix directory that has everything from rootfs ro and the three specific directories (orb/data, store, var) as rw
ROOTFS=/wormhole-rootfs
UPPER=/data/upper
WORK=/data/work
OVERLAY=/mnt/wormhole-overlay
UNIFIED=/mnt/wormhole-unified

REFCOUNT_FILE="/data/refcount"
REFCOUNT_LOCK="/data/refcount.lock"


# NOTE: you should lock before you execute wormhole-attach, not during the run phase because `docker run` returns immediately and the subsequent might `docker exec` might run before mount
mount_wormhole() {
    
    echo "mounting wormhole to $OVERLAY and $UNIFIED"
    mkdir -p $UPPER $WORK $OVERLAY $UNIFIED

    echo "mounting overlayfs"
    mount -t overlay overlay -o lowerdir=$ROOTFS,upperdir=$UPPER,workdir=$WORK $OVERLAY

    # make all rootfs ro except for some specific directories
    echo "creating ro unified mount"
    mount ---bind $ROOTFS $UNIFIED
    mount -o remount,bind,ro $ROOTFS $UNIFIED

    echo "copying over rw files from $OVERLAY to $UNIFIED"
    mount --bind $OVERLAY/nix/store $UNIFIED/nix/store
    mount --bind $OVERLAY/nix/var $UNIFIED/nix/var
    mount --bind $OVERLAY/nix/orb/data $UNIFIED/nix/orb/data

    echo "finished mounting wormhole"
}

touch "$REFCOUNT_FILE" "$REFCOUNT_LOCK"

exec 200>$REFCOUNT_LOCK
flock -x 200

REFCOUNT=$(cat "$REFCOUNT_FILE")
if [ -z "$REFCOUNT" ]; then
    REFCOUNT=0
fi

if [ $REFCOUNT == 0 ]; then 
    mount_wormhole
fi
NEW_REFCOUNT=$((REFCOUNT + 1))
echo "$NEW_REFCOUNT" > "$REFCOUNT_FILE"
echo "Refcount updated from $REFCOUNT to $NEW_REFCOUNT"

exec 200>&-


sleep infinite


