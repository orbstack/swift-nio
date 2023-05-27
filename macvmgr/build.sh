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

# force relinking if Swift lib changed
# if modification time of Swift lib is newer than the binary, relink
LIB_PATH="../swift/GoVZF/.build/${SWIFT_ARCH}-apple-macosx/${BUILD_TYPE:-debug}/libGoVZF.a"
if [[ -f "$VMGR_BIN" ]]; then
    if [[ ! -f "$LIB_PATH" ]] || [[ "$(stat -f "%m" "$LIB_PATH")" -gt "$(stat -f "%m" "$VMGR_BIN")" ]]; then
        rm -f "$VMGR_BIN"
    fi
fi


BUNDLE_OUT="${BUNDLE_OUT:-$PWD/../out/$VMGR_BIN.app}"

rm -fr "$BUNDLE_OUT"
BUNDLE_BIN="$BUNDLE_OUT/Contents/MacOS"
mkdir -p "$BUNDLE_BIN"

go build -ldflags="-extldflags \"$LIB_PATH\" ${EXTRA_LDFLAGS:-}" -o "$BUNDLE_BIN/$VMGR_BIN" "$@"

# make a fake app bundle for embedded.provisionprofile to work
# it checks CFBundleExecutable in Info.plist

# add Info.plist, PkgInfo, and provisioning profile
cp -r bundle/. "$BUNDLE_OUT/Contents"
# initial assets symlink for debug (overwritten by Xcode build for release)
ln -sf ../../../assets "$BUNDLE_OUT/Contents/assets"
# sign bundle w/ resources & executable, vmgr identity + restricted entitlements
codesign --timestamp --options=runtime --entitlements vmgr.entitlements -i dev.kdrag0n.MacVirt.vmgr -s "${SIGNING_CERT:-04B04222116BE16FC0F7DA0E8E1AD338E882A504}" "$BUNDLE_OUT" || :
