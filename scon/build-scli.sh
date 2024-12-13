#!/usr/bin/env bash

set -euo pipefail

cd "$(dirname "$0")"
OUT="../out"

source ../config.sh

SWIFT_ARCH="${SWIFT_ARCH:-arm64}"
SWIFT_BUILD_TYPE="${SWIFT_BUILD_TYPE:-debug}"

rm -fr $OUT/scli.app
mkdir -p $OUT/scli.app/Contents/MacOS
# force cgo when cross-compiling to amd64
CGO_CFLAGS=-mmacosx-version-min=13.0 CGO_ENABLED=1 go build "$@" -ldflags "-extldflags '-mmacosx-version-min=13.0 ../swift/SwExt/.build/$SWIFT_ARCH-apple-macosx/$SWIFT_BUILD_TYPE/libSwExt.a'" -o $OUT/scli.app/Contents/MacOS/scli ./cmd/scli
strip $OUT/scli.app/Contents/MacOS/scli
# add the rest of the fake app bundle
cp -r scli-bundle/. "$OUT/scli.app/Contents"
# this signing ID doesn't matter much
codesign -f --timestamp --options=runtime --entitlements scli.entitlements -i dev.kdrag0n.MacVirt.scli -s "${SIGNING_CERT_OVERRIDE:-$SIGNING_CERT_DEV}" $OUT/scli.app

rm -f $OUT/scli
ln -sf scli.app/Contents/MacOS/scli $OUT/scli
