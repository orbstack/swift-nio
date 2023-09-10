#!/usr/bin/env bash

set -eufo pipefail

cd "$(dirname "$0")"
cd ..

mkdir -p out

#go install github.com/google/go-licenses@latest
#cargo install --locked cargo-about

cp scripts/oss-licenses-template.md out/oss-licenses.md

pushd scon
go-licenses report . --template ../scripts/license-template --ignore github.com/orbstack/macvirt >> ../out/oss-licenses.md
popd

pushd vmgr
go-licenses report . --template ../scripts/license-template --ignore github.com/orbstack/macvirt >> ../out/oss-licenses.md
popd

pushd vinit
cargo about generate about.hbs >> ../out/oss-licenses.md
popd

docker run -i --privileged --rm \
    -v "$PWD/assets:/assets" \
    -v "$PWD/out/oss-licenses.md:/out.md" \
    -v /dev:/hostdev \
    alpine:edge < scripts/gen-licenses-docker.sh

cp out/oss-licenses.md ~/code/web/orbstack-web/docs/docs/legal/oss-licenses.md || :
