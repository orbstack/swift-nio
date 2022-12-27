#!/usr/bin/env bash

cd "$(dirname "$0")"
cd rd
find . | cpio -o -H newc | gzip > ../initrd
cd ..
cp initrd ~/code/android/app/virtproto/app/src/main/assets/initrd
#cp ../linux/kernel ~/code/android/app/virtproto/app/src/main/assets/kernel
cp ~/code/android/kvm/linux/arch/arm64/boot/Image ~/code/android/app/virtproto/app/src/main/assets/kernel
