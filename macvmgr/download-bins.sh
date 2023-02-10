#!/usr/bin/env bash

set -euxo pipefail

DOCKER_ARCH=aarch64
GO_ARCH=arm64

DOCKER_VERSION=23.0.1
BUILDX_VERSION=0.10.2
COMPOSE_VERSION=2.16.0
CREDENTIAL_VERSION=0.7.0

rm -fr xbin
mkdir -p xbin
pushd xbin

# docker
curl -LO https://download.docker.com/mac/static/stable/$DOCKER_ARCH/docker-$DOCKER_VERSION.tgz
tar -xvf docker-${DOCKER_VERSION}.tgz
rm docker-${DOCKER_VERSION}.tgz
mv docker/docker docker_
rm -rf docker
mv docker_ docker

# buildx
# curl -LO https://github.com/docker/buildx/releases/download/v$BUILDX_VERSION/buildx-v$BUILDX_VERSION.darwin-$GO_ARCH
# mv buildx-v$BUILDX_VERSION.darwin-$GO_ARCH docker-buildx
# chmod +x docker-buildx

# compose
curl -LO https://github.com/docker/compose/releases/download/v$COMPOSE_VERSION/docker-compose-darwin-$DOCKER_ARCH
mv docker-compose-darwin-$DOCKER_ARCH docker-compose
chmod +x docker-compose

# docker-credential-osxkeychain
curl -LO https://github.com/docker/docker-credential-helpers/releases/download/v$CREDENTIAL_VERSION/docker-credential-osxkeychain-v$CREDENTIAL_VERSION.darwin-$GO_ARCH
mv docker-credential-osxkeychain-v$CREDENTIAL_VERSION.darwin-$GO_ARCH docker-credential-osxkeychain
chmod +x docker-credential-osxkeychain
