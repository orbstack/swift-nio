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

go build -ldflags="-extldflags \"$LIB_PATH\" ${EXTRA_LDFLAGS:-}" -o "$VMGR_BIN" "$@"

# Apple Development cert
codesign --options runtime -i dev.kdrag0n.MacVirt --entitlements vmgr.entitlements -s 04B04222116BE16FC0F7DA0E8E1AD338E882A504 "$VMGR_BIN" || :
