#!/usr/bin/env bash

set -eufo pipefail
cd "$(dirname "$0")"

export GOPRIVATE='github.com/orbstack/*-macvirt'
BRANCH=orbstack/1.29

orb start

echo "Cloning repos"

cd ../vendor
[[ ! -d "k3s" ]] && git clone -b $BRANCH git@github.com:orbstack/k3s || :

pushd k3s
pushd forks
[[ ! -d "kubernetes" ]] && git clone -b $BRANCH git@github.com:orbstack/kubernetes || :
[[ ! -d "cri-dockerd" ]] && git clone -b $BRANCH git@github.com:orbstack/cri-dockerd || :
popd

echo "Building"
PLATFORMS="amd64 arm64"
for platform in $PLATFORMS; do 
  export DOCKER_DEFAULT_PLATFORM="linux/$platform"
  make

  if [[ "$platform" == "amd64" ]]; then
    cp -f "dist/artifacts/k3s" "../../rootfs/k8s/k3s-$platform"
  else
    cp -f "dist/artifacts/k3s-$platform" ../../rootfs/k8s
  fi
done
