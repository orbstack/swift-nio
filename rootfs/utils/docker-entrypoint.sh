#!/bin/sh

set -e
ip6tables -t nat -A POSTROUTING -s fc00:30:32::/64 -o eth0 -j MASQUERADE
exec dockerd --host=unix:///var/run/docker.sock --tls=false
