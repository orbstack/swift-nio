#!/usr/bin/env bash

cd "$(dirname "$0")"

rm -fr bins
mkdir bins

cp $HOME/code/android/kvm/gvisor-tap-vsock/bin/vm bins/gvproxy-guest
cp ../vcontrol/target/aarch64-unknown-linux-musl/release/vcontrol bins/vcontrol

cp ../vcontainer86/rd/data/etc/ssh/ssh_host_* ./data/etc/ssh/

./pack-disk.sh
