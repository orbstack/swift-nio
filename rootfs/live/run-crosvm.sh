#!/usr/bin/env bash

cd "$(dirname "$0")"
set -euo pipefail

CPUS=8
ASSETS=$PWD/../../assets/debug/amd64
KERNEL=$HOME/code/android/kvm/linux/out86/arch/amd64/boot/bzImage

[[ ! -f data.img ]] && bsdtar -xf $ASSETS/data.img.tar
rm -f swap.img
bsdtar -xf $ASSETS/swap.img.tar

  # --serial type=stdout,hardware=serial,earlycon=true \
  # --balloon-control balloon.sock \
  # --shared-dir $PWD/rd:/dev/root:type=fs:uidmap='0 1000 1':gidmap='0 1000 1' \

  # --host_ip 10.2.2.1 \
  # --netmask 255.255.255.0 \
  # --mac 01:01:01:02:02:02 \
rm -f crosvm.sock
crosvm \
  run \
  --disable-sandbox \
  --cid 4 \
  --mem 2000 \
  --cpus $CPUS \
  --socket crosvm.sock \
  --serial type=stdout,hardware=virtio-console,console=true,stdin=true \
  --disk $ASSETS/rootfs.img,o_direct=true \
  --rwdisk data.img,o_direct=true \
  --rwdisk swap.img,o_direct=true \
  -p "console=hvc0 init=/opt/vc/preinit vc.data_size=2000000 vc.vcontrol_token=test vc.hcontrol_token=test vc.timezone=America/Los_Angeles workqueue.power_efficient=1 cgroup.memory=nokmem,nosocket root=/dev/vda rootfstype=erofs ro" \
  $KERNEL
