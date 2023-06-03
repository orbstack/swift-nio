#!/bin/sh

set -eu

# DinD: a wrapper script which allows docker to be run inside a docker container.
# Original version by Jerome Petazzoni <jerome@docker.com>
# See the blog post: https://blog.docker.com/2013/09/docker-can-now-run-within-docker/
#
# This script should be executed inside a docker container in privileged mode
# ('docker run --privileged', introduced in docker 0.6).

# Usage: dind CMD [ARG...]

# apparmor sucks and Docker needs to know that it's in a container (c) @tianon
export container=docker

if [ -d /sys/kernel/security ] && ! mountpoint -q /sys/kernel/security; then
	mount -t securityfs none /sys/kernel/security || {
		echo >&2 'Could not mount /sys/kernel/security.'
		echo >&2 'AppArmor detection and --privileged mode might break.'
	}
fi

# cgroup v2: enable nesting
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
	# move the processes from the root group to the /init group,
	# otherwise writing subtree_control fails with EBUSY.
	# An error during moving non-existent process (i.e., "cat") is ignored.
	mkdir -p /sys/fs/cgroup/init.scope
	xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init.scope/cgroup.procs || :
	# enable controllers
	sed -e 's/ / +/g' -e 's/^/+/' < /sys/fs/cgroup/cgroup.controllers \
		> /sys/fs/cgroup/cgroup.subtree_control
fi

# Change mount propagation to shared to make the environment more similar to a
# modern Linux system, e.g. with SystemD as PID 1.
mount --make-rshared /

# ------------------------------------

# Docker Desktop compat
mkdir -p /run/host-services
ln -sf /opt/orbstack-guest/run/host-ssh-agent.sock /run/host-services/ssh-auth.sock

ip6tables -t nat -A POSTROUTING -s fd07:b51a:cc66:0001::/64 -o eth0 -j MASQUERADE
export TMPDIR=/dockertmp
# host-gateway-ip: fix https://github.com/orgs/orbstack/discussions/102
exec dockerd --host-gateway-ip=198.19.248.254 --oom-score-adjust=-500
