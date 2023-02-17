#!/usr/bin/env bash

set -eo pipefail
HOME=/home/dragon

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
if [[ $EUID -ne 0 ]]; then
	echo "This script must be run as root" 1>&2
	exit 1
fi

cd "$(dirname "$0")"
pushd out
rm -f rootfs.img
# 64 for ext4
# dd if=/dev/zero of=rootfs.img bs=4M count=64
# mkfs.ext4 rootfs.img
# mount rootfs.img /mnt/tmp
# cp -raf rd/. /mnt/tmp
# umount /mnt/tmp
# trap 'umount /mnt/tmp' EXIT
mkfs.erofs rootfs.img rd -z lz4hc
# cp initrd ~/code/android/app/virtcontainer/app/src/main/assets/initrd
#cp ../linux/kernel ~/code/android/app/virtcontainer/app/src/main/assets/kernel


# data volume
rm -f data.qcow2
qemu-img create -f qcow2 data.qcow2 8T
modprobe nbd max_part=8
qemu-nbd -c /dev/nbd0 data.qcow2
# create gpt partition table; create 1G partition
sfdisk /dev/nbd0 <<EOF
label: gpt
size=1G, type=L, uuid=37d45f5c-49d5-47b4-9a75-fdb70418baf6
EOF
trap 'qemu-nbd -d /dev/nbd0' EXIT
# fast commit, 1% reserved blocks
#mkfs.ext4 -O fast_commit,project -E quotatype=prjquota -m 1 -L user-data-fs /dev/nbd0p1
#mkfs.xfs -L user-data-fs /dev/nbd0p1
#mkfs.f2fs -l user-data-fs -O extra_attr,inode_checksum,sb_checksum,project_quota /dev/nbd0p1
mkfs.btrfs -L user-data-fs -m single -R quota,free-space-tree /dev/nbd0p1

# copy preseed data
mount /dev/nbd0p1 /mnt/tmp
cp -raf data/. /mnt/tmp/
umount /mnt/tmp
qemu-nbd -d /dev/nbd0


# swap volume
rm -f swap.qcow2
qemu-img create -f qcow2 swap.qcow2 64G
qemu-nbd -c /dev/nbd0 swap.qcow2
# create gpt partition table; create two 1G partitions
sfdisk /dev/nbd0 <<EOF
label: gpt
size=1G, type=L, uuid=e071c0ef-c282-439a-a621-8fbd329367dc
size=1G, type=L, uuid=95c2fe16-bb32-478c-adda-16f43d22cffd
EOF
trap 'qemu-nbd -d /dev/nbd0' EXIT
# p1 = zram writeback 1
# p2 = emergency swap
mkswap /dev/nbd0p2
qemu-nbd -d /dev/nbd0

# to raw sparse
qemu-img convert data.qcow2 data.img
qemu-img convert swap.qcow2 swap.img
rm -f data.qcow2 swap.qcow2

# sparse tars
rm -f data.img.tar swap.img.tar
bsdtar -cf data.img.tar data.img
bsdtar -cf swap.img.tar swap.img
rm -f data.img swap.img

copy_file() {
	mkdir -p ../../assets/$BTYPE/$ARCH
	cp "$1" ../../assets/$BTYPE/$ARCH/$2
}

copy_file rootfs.img rootfs.img
if [[ "$ARCH" == "arm64" ]]; then
	copy_file ~/code/android/kvm/linux/out/arch/arm64/boot/Image kernel
else
	copy_file ~/code/android/kvm/linux/out86/arch/x86/boot/bzImage kernel
fi
copy_file data.img.tar data.img.tar
copy_file swap.img.tar swap.img.tar

sudo chown -R dragon:dragon .
