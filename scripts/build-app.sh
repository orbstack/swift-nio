#!/usr/bin/env bash

set -euxo pipefail

#ARCHS=(amd64 arm64)
#NOTARIZE=true
#PUBLISH_UPDATE=true

ARCHS=(amd64 arm64)
NOTARIZE=true
PUBLISH_UPDATE=false

LONG_VER=$(git describe --tags --always --dirty)
SHORT_VER=$(git describe --tag --abbrev=0)
COMMITS=$(git rev-list --count HEAD)

if [[ ! -z "${OVERRIDE_ARCHS:-}" ]]; then
    ARCHS=($OVERRIDE_ARCHS)
    # debug: skip notarization if building for specific archs
    NOTARIZE=false
fi

VMGR_BIN="OrbStack Helper"

cd "$(dirname "$0")/.."
source config.sh

# Apple M1 is ARMv8.4 + most v8.5 extensions (SB, SSBS, CCDP, FRINT3264, SPECRESTRICT, ALTERNATIVENZCV)
# just use v8.4 for simplicity -- we mainly care about specializing for LSE atomics
export GOARM64=v8.4

function build_one() {
    local arch_go="$1"
    local arch_mac=""
    local arch_rust=""
    if [[ "$arch_go" == "amd64" ]]; then
        arch_mac="x86_64"
        arch_rust="x86_64-apple-darwin"
    elif [[ "$arch_go" == "arm64" ]]; then
        arch_mac="arm64"
        arch_rust="aarch64-apple-darwin"
    else
        echo "unknown arch: $arch_go"
        exit 1
    fi

    OUT="$PWD/out"
    rm -fr "$OUT"
    mkdir -p "$OUT"

    # build go (vmgr), rust (virtue), and swift (swext)
    export GOARCH=$arch_go
    pushd vmgr
    BUILD_TYPE=release \
        SIGNING_CERT_OVERRIDE="$SIGNING_CERT" \
        make
    popd

    pushd scon
    rm -f $OUT/scon
    # force cgo when cross-compiling to amd64
    CGO_ENABLED=1 go build -tags release -trimpath -ldflags="-s -w" -o $OUT/scli ./cmd/scli
    strip $OUT/scli
    # this signing ID doesn't matter much
    codesign -f --timestamp --options=runtime -i dev.orbstack.OrbStack.scli -s "$SIGNING_CERT" $OUT/scli
    popd

    # build swift app
    pushd swift

    rm -fr build
    xcodebuild clean

    # copy assets
    mkdir -p build/assets/release
    cp -rc ../assets/release/$arch_go build/assets/release/

    # obfuscate tars to pass notarization
    # (notary service tries to extract 8 TiB sparse tar for analysis)
    pushd build/assets/release/$arch_go
    cat data.img.tar | base64 > data.img.tar.b64
    rm -f data.img.tar
    popd

    # move vmgr dsym out of vmgr bundle
    rm -fr out/$arch_go
    mkdir -p out/$arch_go/dsym
    mv "$OUT/$VMGR_BIN.app/Contents/MacOS/$VMGR_BIN.dSYM" out/$arch_go/dsym/

    # copy bins
    cp -rc ../bins/out/$arch_go build/bins

    xcodebuild archive \
        -scheme MacVirt \
        -arch $arch_mac \
        -archivePath build/app.xcarchive

    # delete assets to avoid slowing down future builds
    rm -fr build/assets/*

    mkdir -p build/$arch_go
    xcodebuild \
        -exportArchive \
        -archivePath build/app.xcarchive \
        -exportOptionsPlist export-options.plist \
        -exportPath build/$arch_go

    mv build/$arch_go/OrbStack.app out/$arch_go/

    cp -r build/app.xcarchive/dSYMs/*.dSYM out/$arch_go/dsym/

    popd
}

rm -fr swift/build

# builds can't be parallel
for arch in "${ARCHS[@]}"; do
    build_one $arch
done

function package_one() {
    local arch="$1"

    # dmg
    # use short ver due to 27 char limit: https://github.com/LinusU/node-alias/issues/7
    local dmg_title="Install OrbStack $SHORT_VER"
    pnpx create-dmg --overwrite --identity="$SIGNING_CERT" --dmg-title="${dmg_title:0:27}" $arch/OrbStack.app $arch

    name="$(basename $arch/*.dmg .dmg)"
    mv $arch/*.dmg "$arch/OrbStack_${LONG_VER}_${COMMITS}_$arch.dmg"
}

function notarize_one() {
    local arch="$1"
    xcrun notarytool submit $arch/*.dmg --keychain-profile main --wait
    xcrun stapler staple $arch/*.dmg
}

pushd swift/out

for arch in "${ARCHS[@]}"; do
    # can't be parallel: may race and fail with "hdiutil: create failed - Resource busy"
    package_one $arch

    if $NOTARIZE; then
        notarize_one $arch &
    fi
done
wait

built_dmgs=(*/*.dmg)

popd

if $PUBLISH_UPDATE; then
    scripts/publish-update.sh "${built_dmgs[@]}"
fi
