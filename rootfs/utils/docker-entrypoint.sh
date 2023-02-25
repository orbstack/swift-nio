#!/bin/sh

set -e
# Docker Desktop compat
mkdir -p /run/host-services
ln -sf /opt/orbstack-guest/run/host-ssh-agent.sock /run/host-services/ssh-auth.sock
ip6tables -t nat -A POSTROUTING -s fc00:30:32::/64 -o eth0 -j MASQUERADE
exec dockerd --host=unix:///var/run/docker.sock --tls=false
