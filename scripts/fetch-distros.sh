#!/usr/bin/env bash

set -eufo pipefail

curl https://images.linuxcontainers.org/streams/v1/images.json | jq -r '.products | keys[]' | cut -d':' -f1,2 | sort | uniq
