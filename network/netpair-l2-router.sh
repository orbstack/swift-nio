#!/usr/bin/env bash

killall udhcpc
ip link set eth0 up
#udhcpc -i eth0

ip link set eth2 up

ip link add name br0 type bridge
ip link set eth0 master br0
ip link set eth2 master br0
#ip addr add 192.168.66.4/24 dev eth2
ip addr add 192.168.66.3/24 dev br0
ip link set eth1 down

ip link set br0 up

ip link set dev eth0 address 86:6c:f1:2e:9f:09


# perf
ip link set br0 txqueuelen 10000
ip link set eth0 txqueuelen 10000
ip link set eth2 txqueuelen 10000
