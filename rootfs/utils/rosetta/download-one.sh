#!/bin/bash

set -euo pipefail

url="$1"
# hash of url = cache key
cache_key="$(echo -n "$url" | b3sum --no-names).pkg"
echo "$cache_key"

if [[ -f "cache/$cache_key" ]]; then
    exit
fi

curl -L "$url" > "cache/$cache_key"
