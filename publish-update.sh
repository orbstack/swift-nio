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
$SPARKLE_BIN/generate_appcast --channel beta --download-url-prefix https://cdn-updates.orbstack.dev/arm64/ --critical-update-version '' --auto-prune-update-files updates/arm64
$SPARKLE_BIN/generate_appcast --channel beta --download-url-prefix https://cdn-updates.orbstack.dev/amd64/ --critical-update-version '' --auto-prune-update-files updates/amd64

# upload to cloudflare
pushd updates

for dmg in "${built_dmgs[@]}"; do
    wrangler r2 object put orbstack-updates/"$dmg" -f "$dmg"
done
#TODO rclone for deltas
wrangler r2 object put orbstack-updates/arm64/appcast.xml -f arm64/appcast.xml
wrangler r2 object put orbstack-updates/amd64/appcast.xml -f amd64/appcast.xml

popd
