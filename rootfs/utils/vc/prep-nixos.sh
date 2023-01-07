#!/usr/bin/env bash

#
# Copyright 2022-2023 Danny Lin <danny@kdrag0n.dev>. All rights reserved.
# 
# Unauthorized copying of this software and associated documentation files (the "Software"), via any medium, is strictly prohibited. Confidential and proprietary.
# 
# The above copyright notice shall be included in all copies or substantial portions of the Software.
#

mkdir -p /data/images
trap 'rm -fr /data/images' EXIT

cd /data/images
wget -O meta.tar.xz https://hydra.nixos.org/build/197965070/download/1/nixos-system-aarch64-linux.tar.xz
wget -O rootfs.tar.xz https://hydra.nixos.org/build/197965105/download/1/nixos-system-aarch64-linux.tar.xz
lxc image import meta.tar.xz rootfs.tar.xz --alias nixos
