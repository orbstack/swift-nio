#!/usr/bin/env bash
set -eux

cd /tmp

# data volume
qemu-img create -f qcow2 data.qcow2 8T
qemu-nbd -c /dev/nbd0 data.qcow2
# partition...
mknod /dev/nbd0p1 b 43 1 || :
mknod /dev/nbd0p2 b 43 2 || :
# create gpt partition table; create 1G partition
sfdisk /dev/nbd0 <<EOF
label: gpt
size=1G, type=L, uuid=37d45f5c-49d5-47b4-9a75-fdb70418baf6
EOF
trap 'qemu-nbd -d /dev/nbd0 || :' EXIT
mkfs.btrfs -L user-data-fs -m single -O block-group-tree -R quota,free-space-tree /dev/nbd0p1

# copy preseed data
mount /dev/nbd0p1 /mnt
echo 1 > /mnt/version
umount /mnt
qemu-nbd -d /dev/nbd0

# wait for qemu-nbd to exit (wait for write flock)
# qemu-nbd just calls ioctl(NBD_DISCONNECT) without waiting for forked daemon to exit
flock data.qcow2 true

# to raw sparse
qemu-img convert data.qcow2 data.img
rm -f data.qcow2

# sparse tars
rm -f data.img.tar
bsdtar -cf data.img.tar data.img
rm -f data.img

# do the build in linux fs and copy out to virtiofs.
# if we do it on virtofs, resulting tars work but are larger (6.5 m / 69k vs. 265k / 19k)
cp -f data.img.tar /out
