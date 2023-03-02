#!/usr/bin/env bash

set -euxo pipefail

LONG_VER=$(git describe --tags --always --dirty)
COMMITS=$(git rev-list --count HEAD)

cd "$(dirname "$0")"

pushd swift/out

built_dmgs=(*/*.dmg)

popd

# updates
SPARKLE_BIN=~/Library/Developer/Xcode/DerivedData/MacVirt-cvlazugpvgfgozfesiozsrqnzfat/SourcePackages/artifacts/sparkle/bin
mkdir -p updates/{arm64,amd64}
cp swift/out/arm64/*.dmg updates/arm64/ || :
cp swift/out/amd64/*.dmg updates/amd64/ || :
COMMON_FLAGS=(--channel beta --critical-update-version '' --auto-prune-update-files --delta-compression lzfse --release-notes-url-prefix 'https://cdn-updates.orbstack.dev/release-notes.html#' --full-release-notes-url 'https://docs.orbstack.dev/release-notes' --maximum-versions 2 --maximum-deltas 3)
$SPARKLE_BIN/generate_appcast "${COMMON_FLAGS[@]}" --download-url-prefix https://cdn-updates.orbstack.dev/arm64/ updates/arm64
$SPARKLE_BIN/generate_appcast "${COMMON_FLAGS[@]}" --download-url-prefix https://cdn-updates.orbstack.dev/amd64/ updates/amd64

# upload to cloudflare
rclone sync updates r2:orbstack-updates
