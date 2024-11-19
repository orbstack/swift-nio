#!/usr/bin/env bash

set -euo pipefail

cmd="$(basename "$0")"
real_tool="$(echo /usr/lib/linux-tools/*-generic/"$cmd")"

if [[ ! -f "$real_tool" ]]; then
    echo "Please install linux-tools-generic to run '$cmd'." >&2
    echo "Run: sudo apt install -y linux-tools-generic" >&2
    exit 1
fi

exec "$real_tool" "$@"
