#!/bin/sh

set -euo pipefail

mkdir /sys/fs/cgroup/init.scope
xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init.scope/cgroup.procs
sed -e 's/ / +/g' -e 's/^/+/' < /sys/fs/cgroup/cgroup.controllers > /sys/fs/cgroup/cgroup.subtree_control

exec /opt/orbstack-guest/simplevisor
