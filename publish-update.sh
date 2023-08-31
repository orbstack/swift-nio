#!/usr/bin/env bash

set -euxo pipefail

cd "$(dirname "$0")"
source config.sh

pushd swift/out
built_dmgs=(*/*.dmg)
popd

# updates
mkdir -p updates/pub/{arm64,amd64}
cp swift/out/arm64/*.dmg updates/pub/arm64/ || :
cp swift/out/amd64/*.dmg updates/pub/amd64/ || :

# upload dsyms
function upload_dsyms() {
    sentry-cli upload-dif --org "$SENTRY_ORG" --project "$SENTRY_PROJECT" "$@"
}
upload_dsyms swift/out/*/dsym/OrbStack.app.dSYM &

# generate appcast
COMMON_FLAGS=(--channel beta --critical-update-version '' --auto-prune-update-files --delta-compression lzfse --release-notes-url-prefix $CDN_BASE_URL'/release-notes.html#' --full-release-notes-url 'https://docs.orbstack.dev/release-notes' --maximum-versions 2 --maximum-deltas 3)
$SPARKLE_BIN/generate_appcast "${COMMON_FLAGS[@]}" --download-url-prefix $CDN_BASE_URL/arm64/ updates/pub/arm64
$SPARKLE_BIN/generate_appcast "${COMMON_FLAGS[@]}" --download-url-prefix $CDN_BASE_URL/amd64/ updates/pub/amd64

# post-process appcasts
scripts/update-appcast.py updates/pub/arm64/appcast.xml
scripts/update-appcast.py updates/pub/amd64/appcast.xml

mkdir -p updates/old/{arm64,amd64}
mv updates/pub/arm64/old_updates/* updates/old/arm64/ || :
mv updates/pub/amd64/old_updates/* updates/old/amd64/ || :

# upload to cloudflare
#rclone sync -P updates/pub r2:orbstack-updates
