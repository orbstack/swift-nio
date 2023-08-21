#!/bin/sh

set -euo pipefail

mkdir /sys/fs/cgroup/init.scope
xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init.scope/cgroup.procs
sed -e 's/ / +/g' -e 's/^/+/' < /sys/fs/cgroup/cgroup.controllers > /sys/fs/cgroup/cgroup.subtree_control

mount --make-rshared /

mkdir /run/host-services
ln -sf /opt/orbstack-guest/run/host-ssh-agent.sock /run/host-services/ssh-auth.sock

ip6tables -t nat -A POSTROUTING -s fd07:b51a:cc66:1::/64 -o eth0 -j MASQUERADE
iptables -t nat -A PREROUTING -s 192.168.215.0/24,192.168.228.0/24,192.168.247.0/24,192.168.207.0/24,192.168.167.0/24,192.168.107.0/24,192.168.237.0/24,192.168.148.0/24,192.168.214.0/24,192.168.165.0/24,192.168.227.0/24,192.168.181.0/24,192.168.158.0/24,192.168.117.0/24,192.168.155.0/24,192.168.194.0/24 -d 172.17.0.1 -j DNAT --to-destination 198.19.249.2
exec /opt/orbstack-guest/simplevisor "dockerd --host-gateway-ip=198.19.248.254" "k3s server --enable-pprof --disable metrics-server,traefik --https-listen-port 6443 --docker --container-runtime-endpoint /var/run/docker.sock --protect-kernel-defaults --flannel-backend host-gw --write-kubeconfig /run/kubeconfig.yml"
