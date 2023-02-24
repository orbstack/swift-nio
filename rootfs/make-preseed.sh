#!/usr/bin/env bash
set -eux

cd /tmp

mount --bind /hostdev /dev

# data volume
truncate -s 8T data.img
losetup -fP data.img
trap "losetup -d /dev/loop0 2>/dev/null || :" EXIT
# create gpt partition table; create 1G partition
sfdisk /dev/loop0 <<EOF
label: gpt
size=1G, type=L, uuid=37d45f5c-49d5-47b4-9a75-fdb70418baf6
EOF
mkfs.btrfs -L user-data-fs -m single -R quota,free-space-tree /dev/loop0p1

# copy preseed data
mount /dev/loop0p1 /mnt
echo 1 > /mnt/version
umount /mnt
losetup -d /dev/loop0


# swap volume
truncate -s 10G swap.img
losetup -fP swap.img
# (already trapped above)
# create gpt partition table; create two 4G partitions
sfdisk /dev/loop0 <<EOF
label: gpt
size=4G, type=L, uuid=e071c0ef-c282-439a-a621-8fbd329367dc
size=4G, type=L, uuid=95c2fe16-bb32-478c-adda-16f43d22cffd
EOF
# p1 = zram writeback 1
# p2 = emergency swap
sleep 10
mkswap /dev/loop0p2
losetup -d /dev/loop0

# sparse tars
bsdtar -cf data.img.tar data.img
bsdtar -cf swap.img.tar swap.img
rm data.img swap.img

# do the build in linux fs and copy out to virtiofs.
# if we do it on virtofs, resulting tars work but are larger (6.5 m / 69k vs. 265k / 19k)
cp data.img.tar swap.img.tar /out
