#!/usr/bin/env bash

ip link set eth2 up
ip addr add 192.168.99.2/24 dev eth2

# route via eth2
ip route del default
ip route add default via 192.168.99.1
