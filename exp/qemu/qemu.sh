#!/usr/bin/env bash

cd "$(dirname "$0")"

  # --serial type=stdout,hardware=serial,earlycon=true \

  # -chardev socket,id=char1,path=/tmp/vhost-fs.sock \
  # -device vhost-user-fs-pci,chardev=char1,tag=rvfs \
#  -nic user,model=virtio-net-pci,hostfwd=tcp::2224-:22 \

CPUS=8

stty intr ^]
qemu-system-aarch64 \
  -machine virt,accel=hvf \
  -smp $CPUS,sockets=1,cores=$CPUS,threads=1 \
  -cpu host \
  -nographic \
  -monitor none \
  -m 8192M \
  -object iothread,id=iothread0 \
  -object iothread,id=iothread1 \
  -object iothread,id=iothread2 \
  -object iothread,id=iothread3 \
  -object iothread,id=iothread4 \
  -serial none \
  -chardev stdio,id=virtiocon0 \
  -device virtio-serial \
  -device virtconsole,chardev=virtiocon0 \
  -nic vmnet-shared,model=virtio-net-pci \
  -device virtio-rng-pci \
  -device virtio-balloon-pci,free-page-hint,free-page-reporting,iothread=iothread4 \
  -append "root=/dev/vda rootfstype=erofs ro init=/opt/vc/preinit console=hvc0 rcu_nocbs=0-7 workqueue.power_efficient=1 cgroup.memory=nokmem,nosocket vc.data_size=65536 vc.vcontrol_token=test vc.timezone=America/Los_Angeles" \
  -device virtio-blk-pci,drive=drive0,id=virtblk0,num-queues=$CPUS,physical_block_size=4096,logical_block_size=4096,iothread=iothread1 \
  -drive file=assets/rootfs.img,if=none,id=drive0,format=raw,discard=on,aio=threads,cache.direct=on \
  -device virtio-blk-pci,drive=drive1,id=virtblk1,num-queues=$CPUS,physical_block_size=4096,logical_block_size=4096,iothread=iothread2 \
  -drive file=assets/data.img,if=none,id=drive1,format=raw,discard=on,aio=threads,cache.direct=on \
  -device virtio-blk-pci,drive=drive2,id=virtblk2,num-queues=$CPUS,physical_block_size=4096,logical_block_size=4096,iothread=iothread3 \
  -drive file=assets/swap.img,if=none,id=drive2,format=raw,discard=on,aio=threads,cache.direct=on \
  -kernel assets/kernel \
  -virtfs local,mount_tag=shared,path=/,security_model=none


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
#   -append "root=/dev/vda rootfstype=erofs ro init=/opt/vc/preinit console=hvc0 rcu_nocbs=0-7 workqueue.power_efficient=1 cgroup.memory=nokmem,nosocket vc.data_size=65536 vc.vcontrol_token=test vc.timezone=America/Los_Angeles" \
#   -device virtio-blk-pci,drive=drive0,id=virtblk0,num-queues=1,physical_block_size=4096,logical_block_size=4096 \
#   -drive file=assets/rootfs.img,if=none,id=drive0,format=raw,discard=on \
#   -device virtio-blk-pci,drive=drive1,id=virtblk1,num-queues=1,physical_block_size=4096,logical_block_size=4096 \
#   -drive file=assets/data.img,if=none,id=drive1,format=raw,discard=on \
#   -device virtio-blk-pci,drive=drive2,id=virtblk2,num-queues=1,physical_block_size=4096,logical_block_size=4096 \
#   -drive file=assets/swap.img,if=none,id=drive2,format=raw,discard=on \
#   -kernel assets/kernel
