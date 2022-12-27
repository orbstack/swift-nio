#!/usr/bin/env bash

set -eo pipefail
HOME=/home/dragon

# require root
if [[ $EUID -ne 0 ]]; then
	echo "This script must be run as root" 1>&2
	exit 1
fi

cd "$(dirname "$0")"

# data volume
rm -f data.qcow2
qemu-img create -f qcow2 data.qcow2 8T
modprobe nbd max_part=8
qemu-nbd -c /dev/nbd0 data.qcow2
# create gpt partition table; create 1G partition
sfdisk /dev/nbd0 <<EOF
label: gpt
,+,L
EOF
trap 'qemu-nbd -d /dev/nbd0' EXIT
# fast commit, 1% reserved blocks
#mkfs.ext4 -O fast_commit -m 1 -L user-data-fs /dev/nbd0p1
#mkfs.xfs -L user-data-fs /dev/nbd0p1
#mkfs.btrfs /dev/nbd0p1
mkfs.f2fs /dev/nbd0p1

# copy preseed data
#mount /dev/nbd0p1 /mnt/tmp
#cp -raf data/. /mnt/tmp/
#umount /mnt/tmp
qemu-nbd -d /dev/nbd0
