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


# swap volume
rm -f swap.qcow2
qemu-img create -f qcow2 swap.qcow2 7G
qemu-nbd -c /dev/nbd0 swap.qcow2
# create gpt partition table; create 4G zram writeback + 2G emergency swap
sfdisk /dev/nbd0 <<EOF
label: gpt
size=4G, type=L, uuid=e071c0ef-c282-439a-a621-8fbd329367dc
size=2G, type=L, uuid=95c2fe16-bb32-478c-adda-16f43d22cffd
EOF
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

# do the build in linux fs and copy out to virtiofs.
# if we do it on virtofs, resulting tars work but are larger (6.5 m / 69k vs. 265k / 19k)
cp -f data.img.tar swap.img.tar /out
