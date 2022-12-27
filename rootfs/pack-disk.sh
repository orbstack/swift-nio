#!/usr/bin/env bash

set -eo pipefail
HOME=/home/dragon

# require root
if [[ $EUID -ne 0 ]]; then
	echo "This script must be run as root" 1>&2
	exit 1
fi

cd "$(dirname "$0")"
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

# qcow2 workaround
qemu-img convert -f raw -O qcow2 rootfs.img rootfs.qcow2
mv rootfs.qcow2 rootfs.img


# data volume
rm -f data.qcow2
qemu-img create -f qcow2 data.qcow2 8T
modprobe nbd max_part=8
qemu-nbd -c /dev/nbd0 data.qcow2
# create gpt partition table; create 1G partition
sfdisk /dev/nbd0 <<EOF
label: gpt
,1G,L
EOF
trap 'qemu-nbd -d /dev/nbd0' EXIT
# fast commit, 1% reserved blocks
#mkfs.ext4 -O fast_commit -m 1 -L user-data-fs /dev/nbd0p1
mkfs.xfs -L user-data-fs /dev/nbd0p1

# copy preseed data
mount /dev/nbd0p1 /mnt/tmp
cp -raf data/. /mnt/tmp/
umount /mnt/tmp
qemu-nbd -d /dev/nbd0

# compact image
# qemu-img convert -c -O qcow2 data.qcow2 data.qcow2.tmp
# mv data.qcow2.tmp data.qcow2
# trap 'rm -f data.qcow2.tmp' EXIT


# swap volume
rm -f swap.qcow2
qemu-img create -f qcow2 swap.qcow2 64G
qemu-nbd -c /dev/nbd0 swap.qcow2
# create gpt partition table; create two 1G partitions
sfdisk /dev/nbd0 <<EOF
label: gpt
,1G,L
,1G,L
EOF
trap 'qemu-nbd -d /dev/nbd0' EXIT
# p1 = zram writeback 1
# p2 = emergency swap
mkswap /dev/nbd0p2
qemu-nbd -d /dev/nbd0

# is release?
if grep -q 'IS_RELEASE=true' build-inside.sh; then
	build_type=release
else
	build_type=debug
fi

copy_file() {
	# location depends on whether it's release
	if [[ $build_type == release ]]; then
		local btype=main
	else
		local btype=debug
	fi

	# cat "$1" | zstd -T0 - > ~/code/android/app/virtcontainer/app/src/main/assets/"$2.zst"
	mkdir -p ~/code/android/app/virtcontainer/app/src/$btype/assets
	cp "$1" ~/code/android/app/virtcontainer/app/src/$btype/assets/$2
}

copy_file rootfs.img rootfs.img
copy_file ~/code/android/kvm/linux/arch/arm64/boot/Image kernel
copy_file data.qcow2 data.qcow2
copy_file swap.qcow2 swap.qcow2
