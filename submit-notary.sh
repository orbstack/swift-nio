#!/usr/bin/env bash

set -euxo pipefail

cd "$(dirname "$0")"
ARCHS="${1:-arm64 amd64}"

# upload dsyms
function submit_one() {
    xcrun notarytool submit "$1" --keychain-profile main --wait
}

for arch in $ARCHS; do
    submit_one swift/out/$arch/*.dmg &
done
