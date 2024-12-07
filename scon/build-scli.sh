#!/usr/bin/env bash

set -euo pipefail

cd "$(dirname "$0")"
OUT="../out"

source ../config.sh

rm -fr $OUT/scli.app
mkdir -p $OUT/scli.app/Contents/MacOS
# force cgo when cross-compiling to amd64
CGO_ENABLED=1 go build "$@" -o $OUT/scli.app/Contents/MacOS/scli ./cmd/scli
strip $OUT/scli.app/Contents/MacOS/scli
# add the rest of the fake app bundle
cp -r scli-bundle/. "$OUT/scli.app/Contents"
# this signing ID doesn't matter much
codesign -f --timestamp --options=runtime --entitlements scli.entitlements -i dev.kdrag0n.MacVirt.scli -s "${SIGNING_CERT_OVERRIDE:-$SIGNING_CERT_DEV}" $OUT/scli.app

rm -f $OUT/scli
ln -sf scli.app/Contents/MacOS/scli $OUT/scli
