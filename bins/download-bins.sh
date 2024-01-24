#!/usr/bin/env bash

set -euxo pipefail

GO_ARCH="${1:-arm64}"
DOCKER_ARCH="${2:-aarch64}"

DOCKER_VERSION=24.0.7
BUILDX_VERSION=0.12.1
COMPOSE_VERSION=2.24.3
CREDENTIAL_VERSION=0.7.0
# match k3s
KUBECTL_VERSION=1.27.4

rm -fr $GO_ARCH
mkdir -p $GO_ARCH
pushd $GO_ARCH

# docker
curl -L https://download.docker.com/mac/static/stable/$DOCKER_ARCH/docker-$DOCKER_VERSION.tgz | tar -xvf -
mv docker/docker docker_
rm -rf docker
mv docker_ docker

# buildx
curl -L https://github.com/docker/buildx/releases/download/v$BUILDX_VERSION/buildx-v$BUILDX_VERSION.darwin-$GO_ARCH > docker-buildx
chmod +x docker-buildx

# compose
curl -L https://github.com/docker/compose/releases/download/v$COMPOSE_VERSION/docker-compose-darwin-$DOCKER_ARCH > docker-compose
chmod +x docker-compose

# docker-credential-osxkeychain
curl -L https://github.com/docker/docker-credential-helpers/releases/download/v$CREDENTIAL_VERSION/docker-credential-osxkeychain-v$CREDENTIAL_VERSION.darwin-$GO_ARCH > docker-credential-osxkeychain
chmod +x docker-credential-osxkeychain

# kubectl
curl -L "https://dl.k8s.io/release/v$KUBECTL_VERSION/bin/darwin/$GO_ARCH/kubectl" > kubectl
chmod +x kubectl
