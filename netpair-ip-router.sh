#!/usr/bin/env bash

# ip forward
echo 1 > /proc/sys/net/ipv4/ip_forward
echo 1 > /proc/sys/net/ipv6/conf/all/forwarding

killall udhcpc
ip link set eth0 up
udhcpc -i eth0

iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE

ip link add bond1 type bond mode balance-rr
ip link set eth2 master bond1
ip link set eth3 master bond1

ip link set bond1 up
ip link set eth2 up
ip link set eth3 up
ip addr add 192.168.99.1/24 dev bond1

ip link set eth1 down
