#!/bin/sh

set -eufo pipefail

apk add syft jq util-linux

mkdir -p /mnt
mount --bind /hostdev /dev # for loop
mount -o ro assets/release/arm64/rootfs.img /mnt
# free loop
trap 'umount /mnt' EXIT

# TODO fix this - go doesn't work but we should be including k3s/moby deps
#SYFT_GOLANG_SEARCH_LOCAL_MOD_CACHE_LICENSES=true SYFT_GOLANG_SEARCH_REMOTE_LICENSES=true \
#syft dir:/mnt -o spdx-json | \
#    jq -r '.packages[] | "- " + .name + ": " + .licenseConcluded + ", " + .licenseDeclared' | \
#    grep -ve "NOASSERTION, NOASSERTION" -e /mnt: > /tmp.md

syft dir:/mnt -o spdx-json | \
    jq -r '.packages[] | "- " + .name + ": " + .licenseDeclared' | \
    grep -ve NOASSERTION -e /mnt: > /tmp.md

cat /tmp.md | sort | uniq >> /out.md
