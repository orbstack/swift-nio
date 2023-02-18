#!/usr/bin/env bash

set -euxo pipefail

#ARCHS=(amd64 arm64)
ARCHS=(arm64)
#PUBLISH_UPDATE=true
PUBLISH_UPDATE=false

LONG_VER=$(git describe --tags --always --dirty)
COMMITS=$(git rev-list --count HEAD)

cd "$(dirname "$0")"

function build_one() {
    local arch_go="$1"
    local arch_mac=""
    if [[ "$arch_go" == "amd64" ]]; then
        arch_mac="x86_64"
    elif [[ "$arch_go" == "arm64" ]]; then
        arch_mac="arm64"
    else
        echo "unknown arch: $arch_go"
        exit 1
    fi

    # build go (vmgr and scon)
    export GOARCH=$arch_go
    export CGO_ENABLED=1

    OUT=./

    pushd macvmgr
    go generate ./conf/appver ./drm/killswitch
    go build -tags release -trimpath -ldflags="-s -w" -o $OUT/macvmgr
    codesign -f --timestamp --options=runtime --entitlements vmm.entitlements -s ECD9A0D787DFCCDD0DB5FF21CD2F6666B9B5ADC2 $OUT/macvmgr
    popd

    pushd scon
    go build -tags release -trimpath -ldflags="-s -w" -o $OUT/scli ./cmd/scli
    codesign -f --timestamp --options=runtime -s ECD9A0D787DFCCDD0DB5FF21CD2F6666B9B5ADC2 $OUT/scli
    popd

    # TODO: rebuild rootfs

    # build swift
    pushd swift

    rm -fr build
    xcodebuild clean

    # copy assets
    mkdir -p build/assets/release
    cp -rc ../assets/release/$arch_go build/assets/release/

    # obfuscate tars to pass notarization
    pushd build/assets/release/$arch_go
    cat data.img.tar | base64 > data.img.tar.b64
    cat swap.img.tar | base64 > swap.img.tar.b64
    rm -f data.img.tar swap.img.tar
    popd

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

    rm -fr build/app.xcarchive

    mkdir -p out/$arch_go
    mv build/$arch_go/OrbStack.app out/$arch_go/

    popd
}

rm -fr swift/{build,out}

# builds can't be parallel
for arch in "${ARCHS[@]}"; do
    build_one $arch
done

function package_one() {
    local arch="$1"

    # dmg
    create-dmg --overwrite $arch/OrbStack.app $arch

    # notarize
    xcrun notarytool submit $arch/*.dmg --keychain-profile main --wait

    # staple
    xcrun stapler staple $arch/*.dmg

    name="$(basename $arch/*.dmg .dmg)"
    mv $arch/*.dmg "$arch/OrbStack_${LONG_VER}_${COMMITS}_$arch.dmg"
}

pushd swift/out

for arch in "${ARCHS[@]}"; do
    package_one $arch &
done
wait

built_dmgs=(*/*.dmg)

popd

if $PUBLISH_UPDATE; then
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

fi
