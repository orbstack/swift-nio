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

go generate ./conf/appver ./drm/killswitch

CGO_CFLAGS="-mmacosx-version-min=12.3" \
CGO_LDFLAGS="-mmacosx-version-min=12.3" \
go build -buildmode=pie -ldflags="-extldflags \"$LIB_PATH\" ${EXTRA_LDFLAGS:-}" -o "$BIN_OUT" "$@"

# strip for release
if [[ "${BUILD_TYPE:-debug}" == "release" ]]; then
    strip "$BIN_OUT"
fi

# make a fake app bundle for embedded.provisionprofile to work
# it checks CFBundleExecutable in Info.plist

# add Info.plist, PkgInfo, and provisioning profile
# -R doesn't follow symlinks
cp -R bundle/. "$BUNDLE_OUT/Contents"
# sign bundle w/ resources & executable, vmgr identity + restricted entitlements
codesign -f --timestamp --options=runtime --entitlements vmgr.entitlements -i dev.kdrag0n.MacVirt.vmgr -s "$SIGNING_CERT_DEV" "$BUNDLE_OUT" || :
