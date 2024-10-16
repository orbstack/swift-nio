#!/usr/bin/env bash

set -euxo pipefail

cd "$(dirname "$0")/.."
source config.sh

# get version from arm64 dmg name
VERSION=$(ls swift/out/arm64/*.dmg | sed -E 's/.*OrbStack_v([0-9.]+.*)_arm64.dmg/\1/')

# updates
mkdir -p updates/pub/{arm64,amd64} updates/dsym
cp swift/out/arm64/*.dmg updates/pub/arm64/ || :
cp swift/out/amd64/*.dmg updates/pub/amd64/ || :

# upload dsyms to sentry
function upload_sentry_dsyms() {
    sentry-cli upload-dif --org "$SENTRY_ORG" --project "$SENTRY_PROJECT" "$@"
}
upload_sentry_dsyms swift/out/*/dsym/OrbStack.app.dSYM &

# package all dsyms for internal use
pushd swift/out
tar --zstd -cf ../../updates/dsym/$VERSION.tar.zst */dsym
popd

# skip delta generation for canary
VERSION_TAG="$(git describe --tag --abbrev=0)"
MAX_DELTAS=3
if [[ "$VERSION_TAG" == *"-rc"* ]]; then
    MAX_DELTAS=0
fi

# generate appcast
# TODO support marking as critical
CRITICAL_FLAGS=(--critical-update-version '')
COMMON_FLAGS=(--channel beta --auto-prune-update-files --delta-compression lzfse --release-notes-url-prefix $CDN_BASE_URL'/release-notes.html#' --full-release-notes-url 'https://docs.orbstack.dev/release-notes' --maximum-versions 2 --maximum-deltas "$MAX_DELTAS")
$SPARKLE_BIN/generate_appcast "${COMMON_FLAGS[@]}" --download-url-prefix $CDN_BASE_URL/arm64/ updates/pub/arm64
$SPARKLE_BIN/generate_appcast "${COMMON_FLAGS[@]}" --download-url-prefix $CDN_BASE_URL/amd64/ updates/pub/amd64

# post-process appcasts
scripts/update-appcast.py updates/pub/arm64/appcast.xml
scripts/update-appcast.py updates/pub/amd64/appcast.xml

mkdir -p updates/old/{arm64,amd64}
mv updates/pub/arm64/old_updates/* updates/old/arm64/ || :
mv updates/pub/amd64/old_updates/* updates/old/amd64/ || :
