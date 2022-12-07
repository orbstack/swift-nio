#!/usr/bin/env bash

cd "$(dirname "$0")"
cd assets

qemu-img convert data.qcow2 data.img
rm -f data.qcow2

qemu-img convert swap.qcow2 swap.img
rm -f swap.qcow2

qemu-img convert rootfs.img rootfs.img.tmp
mv rootfs.img.tmp rootfs.img
