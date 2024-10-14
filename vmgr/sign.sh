#!/usr/bin/env bash

set -euo pipefail
cd "$(dirname "$0")"

# default dev signing cert
source ../config.sh

SIGNING_CERT="${SIGNING_CERT_OVERRIDE:-$SIGNING_CERT_DEV}"

# strip for release
codesign_flags=(-f)
if [[ "$GO_BUILD_TYPE" == "release" ]]; then
    strip "$VMGR_BIN"
    # only need timestamp for release notarization
    # omit in debug to allow working offline
    codesign_flags+=(--timestamp)
fi

# make a fake app bundle for embedded.provisionprofile to work
# it checks CFBundleExecutable in Info.plist

# add Info.plist, PkgInfo, and provisioning profile
# -R doesn't follow symlinks
cp -R bundle/. "$VMGR_BUNDLE/Contents"
# sign bundle with resources & executable, vmgr identity + restricted entitlements
codesign "${codesign_flags[@]}" --options=runtime --entitlements "vmgr.$GO_BUILD_TYPE.entitlements" -i dev.kdrag0n.MacVirt.vmgr -s "$SIGNING_CERT" "$VMGR_BUNDLE" || :

# for xcode app debug build - assets loaded from symlinked debug bundle
mkdir -p ../swift/build/assets
