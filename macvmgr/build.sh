#!/usr/bin/env bash

set -euo pipefail
cd "$(dirname "$0")"

#go build -ldflags '-extldflags "-sectcreate __TEXT __info_plist /Users/dragon/Library/Developer/Xcode/DerivedData/MacVirt-cvlazugpvgfgozfesiozsrqnzfat/Build/Products/Debug/OrbStack.app/Contents/Info.plist"' "$@"

# force relinking if Swift lib changed
# if modification time of Swift lib is newer than the binary, relink
LIB_PATH="../swift/build/${GOARCH:-arm64}/libgovzf.a"
if [[ -f macvmgr ]]; then
    if [[ ! -f "$LIB_PATH" ]] || [[ "$(stat -f "%m" "$LIB_PATH")" -gt "$(stat -f "%m" macvmgr)" ]]; then
        rm -f macvmgr
    fi
fi

go build -ldflags="-extldflags \"$LIB_PATH\"" "$@"

# Apple Development cert
codesign --entitlements vmgr.entitlements -s 04B04222116BE16FC0F7DA0E8E1AD338E882A504 macvmgr || :
