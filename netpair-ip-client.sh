#!/usr/bin/env bash


ip link add bond1 type bond mode balance-rr
ip link set eth2 master bond1
ip link set eth3 master bond1

ip link set bond1 up
ip link set eth2 up
ip link set eth3 up
ip addr add 192.168.99.2/24 dev bond1

# route via eth2
ip route del default
ip route add default via 192.168.99.1
