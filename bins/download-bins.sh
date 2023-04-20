#!/usr/bin/env bash

set -euxo pipefail

GO_ARCH="${1:-arm64}"
DOCKER_ARCH="${2:-aarch64}"

DOCKER_VERSION=23.0.4
BUILDX_VERSION=0.10.4
COMPOSE_VERSION=2.17.2
CREDENTIAL_VERSION=0.7.0

rm -fr $GO_ARCH
mkdir -p $GO_ARCH
pushd $GO_ARCH

# docker
curl -LO https://download.docker.com/mac/static/stable/$DOCKER_ARCH/docker-$DOCKER_VERSION.tgz
tar -xvf docker-${DOCKER_VERSION}.tgz
rm docker-${DOCKER_VERSION}.tgz
mv docker/docker docker_
rm -rf docker
mv docker_ docker

# buildx
curl -LO https://github.com/docker/buildx/releases/download/v$BUILDX_VERSION/buildx-v$BUILDX_VERSION.darwin-$GO_ARCH
mv buildx-v$BUILDX_VERSION.darwin-$GO_ARCH docker-buildx
chmod +x docker-buildx

# compose
curl -LO https://github.com/docker/compose/releases/download/v$COMPOSE_VERSION/docker-compose-darwin-$DOCKER_ARCH
mv docker-compose-darwin-$DOCKER_ARCH docker-compose
chmod +x docker-compose

# docker-credential-osxkeychain
curl -LO https://github.com/docker/docker-credential-helpers/releases/download/v$CREDENTIAL_VERSION/docker-credential-osxkeychain-v$CREDENTIAL_VERSION.darwin-$GO_ARCH
mv docker-credential-osxkeychain-v$CREDENTIAL_VERSION.darwin-$GO_ARCH docker-credential-osxkeychain
chmod +x docker-credential-osxkeychain
