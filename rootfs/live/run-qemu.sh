#!/usr/bin/env bash

cd "$(dirname "$0")"
set -euo pipefail

CPUS=8
ASSETS=$PWD/../../assets/debug/amd64
KERNEL=$HOME/code/android/kvm/linux/out86/arch/amd64/boot/bzImage

[[ ! -f data.img ]] && bsdtar -xf $ASSETS/data.img.tar
rm -f swap.img
bsdtar -xf $ASSETS/swap.img.tar

stty intr ^]
trap 'stty intr ^C' EXIT

  # --serial type=stdout,hardware=serial,earlycon=true \

  # -chardev socket,id=char1,path=/tmp/vhost-fs.sock \
  # -device vhost-user-fs-pci,chardev=char1,tag=rvfs \
#  -nic user,model=virtio-net-pci,hostfwd=tcp::2224-:22 \

qemu-system-x86_64 \
  -machine q35,accel=kvm \
  -smp $CPUS,sockets=1,cores=$CPUS,threads=1 \
  -cpu host \
  -nographic \
  -monitor none \
  -m 6144M \
  -object iothread,id=iothread0 \
  -object iothread,id=iothread1 \
  -object iothread,id=iothread2 \
  -object iothread,id=iothread3 \
  -object iothread,id=iothread4 \
  -serial none \
  -chardev stdio,id=virtiocon0 \
  -device virtio-serial \
  -device virtconsole,chardev=virtiocon0 \
  -nic user,model=virtio-net-pci,hostfwd=tcp::2222-:22 \
  -device virtio-rng-pci \
  -device virtio-balloon-pci,iothread=iothread4 \
  -append "console=hvc0 init=/opt/orb/preinit orb.data_size=2000000 workqueue.power_efficient=1 cgroup.memory=nokmem,nosocket root=/dev/vda rootfstype=erofs ro" \
  -device virtio-blk-pci,drive=drive0,id=virtblk0,num-queues=$CPUS,iothread=iothread1 \
  -drive file=$ASSETS/rootfs.img,if=none,id=drive0,format=raw,discard=on,aio=threads,cache=none,cache.direct=on,readonly=on \
  -device virtio-blk-pci,drive=drive1,id=virtblk1,num-queues=$CPUS,iothread=iothread2 \
  -drive file=data.img,if=none,id=drive1,format=raw,discard=on,aio=threads,cache=none,cache.direct=on \
  -device virtio-blk-pci,drive=drive2,id=virtblk2,num-queues=$CPUS,iothread=iothread3 \
  -drive file=swap.img,if=none,id=drive2,format=raw,discard=on,aio=threads,cache=none,cache.direct=on \
  -kernel $KERNEL \
  -virtfs local,mount_tag=shared,path=$HOME/code/projects/macvirt,security_model=none


#-device qemu-xhci,id=usb-bus
#-drive file=/Users/dragon/.lima/default/diffdisk,if=virtio,discard=on
#-netdev user,id=net0,net=192.168.5.0/24,dhcpstart=192.168.5.15,hostfwd=tcp:127.0.0.1:60022-:22
#-device virtio-net-pci,netdev=net0,mac=52:55:55:2c:d9:75
#-chardev socket,id=char-serial,path=/Users/dragon/.lima/default/serial.sock,server=on,wait=off,logfile=/Users/dragon/.lima/default/serial.log
#-serial chardev:char-serial
#-chardev socket,id=char-qmp,path=/Users/dragon/.lima/default/qmp.sock,server=on,wait=off
#-qmp chardev:char-qmp
#-name lima-default
#-pidfile /Users/dragon/.lima/default/qemu.pid

# qemu-system-aarch64 \
#   -machine virt,accel=hvf \
#   -smp $CPUS,sockets=1,cores=$CPUS,threads=1 \
#   -cpu host \
#   -nographic \
#   -monitor none \
#   -m 8192M \
#   -serial none \
#   -chardev stdio,id=virtiocon0 \
#   -device virtio-serial \
#   -device virtconsole,chardev=virtiocon0 \
#   -nic user,model=virtio-net-pci,hostfwd=tcp::2222-:22 \
#   -device virtio-rng-pci \
#   -device virtio-balloon-pci \
#   -append "root=/dev/vda rootfstype=erofs ro init=/opt/orb/preinit console=hvc0 rcu_nocbs=0-7 workqueue.power_efficient=1 cgroup.memory=nokmem,nosocket orb.data_size=65536" \
#   -device virtio-blk-pci,drive=drive0,id=virtblk0,num-queues=1,physical_block_size=4096,logical_block_size=4096 \
#   -drive file=assets/rootfs.img,if=none,id=drive0,format=raw,discard=on \
#   -device virtio-blk-pci,drive=drive1,id=virtblk1,num-queues=1,physical_block_size=4096,logical_block_size=4096 \
#   -drive file=assets/data.img,if=none,id=drive1,format=raw,discard=on \
#   -device virtio-blk-pci,drive=drive2,id=virtblk2,num-queues=1,physical_block_size=4096,logical_block_size=4096 \
#   -drive file=assets/swap.img,if=none,id=drive2,format=raw,discard=on \
#   -kernel assets/kernel
