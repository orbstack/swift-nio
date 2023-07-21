#!/usr/bin/env bash

set -eo pipefail

cd "$(dirname "$0")"

docker build --platform linux/arm64 -f migration/Dockerfile .. -t ghcr.io/orbstack/dmigrate-agent:1
docker build --platform linux/amd64 -f migration/Dockerfile .. -t ghcr.io/orbstack/dmigrate-agent:1

#docker push ghcr.io/orbstack/dmigrate-agent:1
