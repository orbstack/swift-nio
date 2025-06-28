#!/usr/bin/env bash

set -euxo pipefail

GO_ARCH="${1:-arm64}"
DOCKER_ARCH="${2:-aarch64}"

DOCKER_VERSION=28.3.0
BUILDX_VERSION=0.25.0
COMPOSE_VERSION=2.37.3
CREDENTIAL_VERSION=0.9.3
# match k3s
KUBECTL_VERSION=1.30.7

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

# docker cli completions
popd
rm -fr completions
mkdir -p completions
pushd completions
mkdir -p zsh fish bash
curl -L "https://raw.githubusercontent.com/docker/cli/master/contrib/completion/bash/docker" > bash/docker.bash
curl -L "https://raw.githubusercontent.com/docker/cli/master/contrib/completion/fish/docker.fish" > fish/docker.fish
curl -L "https://raw.githubusercontent.com/docker/cli/master/contrib/completion/zsh/_docker" > zsh/_docker

# kubectl completions
../$GO_ARCH/kubectl completion bash > bash/kubectl.bash
../$GO_ARCH/kubectl completion zsh > zsh/_kubectl
../$GO_ARCH/kubectl completion fish > fish/kubectl.fish

# also generate orb and orbctl completions (ok if this fails)
set +e
(exec -a orbctl ../../../scon/scli completion bash) > bash/orbctl.bash
(exec -a orbctl ../../../scon/scli completion zsh) > zsh/_orbctl
(exec -a orbctl ../../../scon/scli completion fish) > fish/orbctl.fish

# only zsh needs a separate file for orb
(exec -a orb ../../../scon/scli completion zsh) > zsh/_orb
set -e

# historically we crammed all completions into the same dir except zsh, so symlink them for compatibility
ln -sf bash/docker.bash bash/kubectl.bash fish/docker.fish fish/kubectl.fish .
