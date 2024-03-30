#!/usr/bin/env bash

set -euo pipefail
cd "$(dirname "$0")"

VMGR_BIN="OrbStack Helper"

# default dev signing cert
source ../config.sh

# translate Go to Swift arch
if [[ "${GOARCH:-arm64}" == "arm64" ]]; then
    GOARCH="arm64"
    SWIFT_ARCH="arm64"
else
    GOARCH="amd64"
    SWIFT_ARCH="x86_64"
fi

SIGNING_CERT="${SIGNING_CERT_OVERRIDE:-$SIGNING_CERT_DEV}"

BUNDLE_OUT="${BUNDLE_OUT:-$PWD/../out/$VMGR_BIN.app}"
BUNDLE_BIN="$BUNDLE_OUT/Contents/MacOS"
BIN_OUT="$BUNDLE_BIN/$VMGR_BIN"
mkdir -p "$BUNDLE_BIN"

# force relinking if Swift lib changed
# if modification time of Swift lib is newer than the binary, relink
SWIFT_LIB_PATH="../swift/GoVZF/.build/${SWIFT_ARCH}-apple-macosx/${BUILD_TYPE:-debug}/libGoVZF.a"
if [[ -f "$BIN_OUT" ]]; then
    if [[ ! -f "$SWIFT_LIB_PATH" ]] || [[ "$(stat -f "%m" "$SWIFT_LIB_PATH")" -gt "$(stat -f "%m" "$BIN_OUT")" ]]; then
        rm -f "$BIN_OUT"
    fi
fi

# same logic for Rust lib
RUST_LIB_PATH="$HOME/code/vm/libkrun/target/${RUST_BUILD_TYPE:-debug}/libkrun.a"
if [[ -f "$BIN_OUT" ]]; then
    if [[ ! -f "$RUST_LIB_PATH" ]] || [[ "$(stat -f "%m" "$RUST_LIB_PATH")" -gt "$(stat -f "%m" "$BIN_OUT")" ]]; then
        rm -f "$BIN_OUT"
    fi
fi

go generate ./conf/appver ./drm/killswitch

CGO_CFLAGS="-mmacosx-version-min=12.3" \
CGO_LDFLAGS="-mmacosx-version-min=12.3" \
go build -buildmode=pie -ldflags="-extldflags \"$SWIFT_LIB_PATH $RUST_LIB_PATH\" ${EXTRA_LDFLAGS:-}" -o "$BIN_OUT" "$@"

# strip for release
codesign_flags=(-f)
if [[ "${BUILD_TYPE:-debug}" == "release" ]]; then
    strip "$BIN_OUT"
    # only need timestamp for release notarization
    # omit in debug to allow working offline
    codesign_flags+=(--timestamp)
fi

# make a fake app bundle for embedded.provisionprofile to work
# it checks CFBundleExecutable in Info.plist

# add Info.plist, PkgInfo, and provisioning profile
# -R doesn't follow symlinks
cp -R bundle/. "$BUNDLE_OUT/Contents"
# sign bundle w/ resources & executable, vmgr identity + restricted entitlements
codesign "${codesign_flags[@]}" --options=runtime --entitlements vmgr.entitlements -i dev.kdrag0n.MacVirt.vmgr -s "$SIGNING_CERT" "$BUNDLE_OUT" || :

# for xcode app debug build - assets loaded from symlinked debug bundle
mkdir -p ../swift/build/assets
