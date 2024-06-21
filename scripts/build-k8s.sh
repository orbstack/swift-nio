#!/usr/bin/env bash

set -eufo pipefail
cd "$(dirname "$0")"

export GOPRIVATE='github.com/orbstack/*-macvirt'
BRANCH=orbstack/1.29

orb start

echo "Cloning repos"

cd ../vendor
if [[ ! -d "k3s" ]]; then
  git clone -b $BRANCH git@github.com:orbstack/k3s
fi

pushd k3s
pushd forks
if [[ ! -d "kubernetes" ]]; then
  git clone -b $BRANCH git@github.com:orbstack/kubernetes
fi
if [[ ! -d "cri-dockerd" ]]; then
  git clone -b $BRANCH git@github.com:orbstack/cri-dockerd
fi
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
