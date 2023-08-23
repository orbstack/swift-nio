#!/bin/sh

set -euo pipefail

mkdir /sys/fs/cgroup/init.scope
xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init.scope/cgroup.procs
sed -e 's/ / +/g' -e 's/^/+/' < /sys/fs/cgroup/cgroup.controllers > /sys/fs/cgroup/cgroup.subtree_control

mount --make-rshared /

mkdir /run/host-services
ln -sf /opt/orbstack-guest/run/host-ssh-agent.sock /run/host-services/ssh-auth.sock

iptables -t nat -N ORB-PREROUTING
iptables -t nat -A PREROUTING -j ORB-PREROUTING
iptables -t nat -A ORB-PREROUTING -s 192.168.215.0/24,192.168.228.0/24,192.168.247.0/24,192.168.207.0/24,192.168.167.0/24,192.168.107.0/24,192.168.237.0/24,192.168.148.0/24,192.168.214.0/24,192.168.165.0/24,192.168.227.0/24,192.168.181.0/24,192.168.158.0/24,192.168.117.0/24,192.168.155.0/24 -d 172.17.0.1 -j DNAT --to-destination 198.19.249.2

ip6tables -t nat -N ORB-POSTROUTING
ip6tables -t nat -A POSTROUTING -j ORB-POSTROUTING
ip6tables -t nat -N ORB-POSTROUTING-S1
ip6tables -t nat -A ORB-POSTROUTING-S1 '!' -s fd07:b51a:cc66::/64 -j MASQUERADE
ip6tables -t nat -A ORB-POSTROUTING -s fc00::/7 -o eth0 -j ORB-POSTROUTING-S1

exec /opt/orbstack-guest/simplevisor
