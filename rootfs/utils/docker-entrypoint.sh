#!/bin/sh

set -euo pipefail

mkdir /sys/fs/cgroup/init.scope
xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init.scope/cgroup.procs
sed -e 's/ / +/g' -e 's/^/+/' < /sys/fs/cgroup/cgroup.controllers > /sys/fs/cgroup/cgroup.subtree_control

mount --make-rshared /

mkdir -p /run/host-services
ln -sf /opt/orbstack-guest/run/host-ssh-agent.sock /run/host-services/ssh-auth.sock

ip6tables -t nat -A POSTROUTING -s fd07:b51a:cc66:0001::/64 -o eth0 -j MASQUERADE
export TMPDIR=/dockertmp
exec dockerd --host-gateway-ip=198.19.248.254
