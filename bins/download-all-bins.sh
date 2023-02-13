#!/usr/bin/env bash

set -euxo pipefail

cd "$(dirname "$0")"

rm -fr out
mkdir -p out
pushd out

../download-bins.sh arm64 aarch64 &
../download-bins.sh amd64 x86_64 &
wait
