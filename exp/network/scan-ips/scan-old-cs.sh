#!/bin/bash

set -eufo pipefail

SCAN_NETS=()

# add 192.168.1. - 192.168.254. to array
for i in $(seq 106 254); do
    SCAN_NETS+=("192.168.$i.")
done

# add 172.16. - 172.31. to array
# avoid 172.30 because earthly
for i in $(seq 16 31); do
    SCAN_NETS+=("172.$i.")
done

# add 10.1. - 10.254. to array
for i in $(seq 1 254); do
    SCAN_NETS+=("10.$i.")
done

# search them all
for subnet in "${SCAN_NETS[@]}"; do
    echo -n "$subnet,"
    curl -s -L \
        -H "Accept: application/vnd.github+json" \
        -H "Authorization: Bearer XXXXXXX"\
        -H "X-GitHub-Api-Version: 2022-11-28" \
        "https://api.github.com/search/code?q=$subnet" | jq .total_count
    # rate limit...
    sleep 30
done
