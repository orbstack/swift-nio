#!/bin/bash

set -euo pipefail

url="$1"
# hash of url = cache key
cache_key="$(echo -n "$url" | b3sum --no-names).pkg"
echo "$cache_key"

if [[ -f "cache/$cache_key" ]]; then
    exit
fi

# only move on successful download
curl -L "$url" > "/tmp/$cache_key"
mv "/tmp/$cache_key" "cache/$cache_key"
