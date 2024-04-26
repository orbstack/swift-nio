#!/usr/bin/env bash

set -eufo pipefail
cd "$(dirname "$0")"

export GOPRIVATE='github.com/orbstack/*-macvirt'
BRANCH=orbstack/1.29
ARCH="$(uname -m)"

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
make

cp -f "dist/artifacts/k3s-$ARCH" ../../rootfs/k8s
