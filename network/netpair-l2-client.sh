#!/usr/bin/env bash

ip link set eth2 up
ip addr add 192.168.66.5/24 dev eth2

# route via eth2
ip route del default
ip route add default via 192.168.66.1

ip link set dev eth2 address 86:6c:f1:2e:9f:00


# perf
ip link set eth2 txqueuelen 10000
