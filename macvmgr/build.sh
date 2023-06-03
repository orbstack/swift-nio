#!/usr/bin/env bash

set -euo pipefail
cd "$(dirname "$0")"

VMGR_BIN="OrbStack Helper (VM)"

# translate Go to Swift arch
if [[ "${GOARCH:-arm64}" == "arm64" ]]; then
    GOARCH="arm64"
    SWIFT_ARCH="arm64"
else
    GOARCH="amd64"
    SWIFT_ARCH="x86_64"
fi

BUNDLE_OUT="${BUNDLE_OUT:-$PWD/../out/$VMGR_BIN.app}"
BUNDLE_BIN="$BUNDLE_OUT/Contents/MacOS"
BIN_OUT="$BUNDLE_BIN/$VMGR_BIN"
mkdir -p "$BUNDLE_BIN"

# force relinking if Swift lib changed
# if modification time of Swift lib is newer than the binary, relink
LIB_PATH="../swift/GoVZF/.build/${SWIFT_ARCH}-apple-macosx/${BUILD_TYPE:-debug}/libGoVZF.a"
if [[ -f "$BIN_OUT" ]]; then
    if [[ ! -f "$LIB_PATH" ]] || [[ "$(stat -f "%m" "$LIB_PATH")" -gt "$(stat -f "%m" "$BIN_OUT")" ]]; then
        rm -f "$BIN_OUT"
    fi
fi

go build -ldflags="-extldflags \"$LIB_PATH\" ${EXTRA_LDFLAGS:-}" -o "$BIN_OUT" "$@"

# make a fake app bundle for embedded.provisionprofile to work
# it checks CFBundleExecutable in Info.plist

# add Info.plist, PkgInfo, and provisioning profile
cp -r bundle/. "$BUNDLE_OUT/Contents"
# initial assets symlink for debug (overwritten by Xcode build for release)
ln -sf ../../../assets "$BUNDLE_OUT/Contents/assets"
# sign bundle w/ resources & executable, vmgr identity + restricted entitlements
codesign -f --timestamp --options=runtime --entitlements vmgr.entitlements -i dev.kdrag0n.MacVirt.vmgr -s "${SIGNING_CERT:-F14BEB1D721604BE6C984703AF6C88E1F8F35832}" "$BUNDLE_OUT" || :
