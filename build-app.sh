#!/usr/bin/env bash

set -euxo pipefail

cd "$(dirname "$0")"

function build_one() {
    local arch_go="$1"
    local arch_mac="$2"

    # build go (vmgr and scon)
    export GOARCH=$arch_go
    export CGO_ENABLED=1

    OUT=./

    pushd macvmgr
    go generate ./conf/appver
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

# # builds can't be parallel
build_one amd64 x86_64
build_one arm64 arm64

function package_one() {
    local arch="$1"

    # dmg
    create-dmg --overwrite $arch/OrbStack.app $arch

    # notarize
    #xcrun notarytool submit $arch/*.dmg --keychain-profile main --wait

    # staple
    #xcrun stapler staple $arch/*.dmg

    name="$(basename $arch/*.dmg .dmg)"
    mv $arch/*.dmg "$arch/$name $arch.dmg"
}

pushd swift/out

package_one amd64 &
package_one arm64 &
wait

built_dmgs=(*/*.dmg)

popd

# updates
SPARKLE_BIN=~/Library/Developer/Xcode/DerivedData/MacVirt-cvlazugpvgfgozfesiozsrqnzfat/SourcePackages/artifacts/sparkle/bin
mkdir -p updates/beta/{arm64,amd64}
cp swift/out/arm64/*.dmg updates/beta/arm64/ || :
cp swift/out/amd64/*.dmg updates/beta/amd64/ || :
$SPARKLE_BIN/generate_appcast --download-url-prefix https://cdn-updates.orbstack.dev/beta/arm64/ --critical-update-version '' --auto-prune-update-files updates/beta/arm64
$SPARKLE_BIN/generate_appcast --download-url-prefix https://cdn-updates.orbstack.dev/beta/amd64/ --critical-update-version '' --auto-prune-update-files updates/beta/amd64

# upload to cloudflare
pushd updates/beta

for dmg in "${built_dmgs[@]}"; do
    wrangler r2 object put orbstack-updates/beta/"$dmg" -f "$dmg"
done
wrangler r2 object put orbstack-updates/beta/arm64/appcast.xml -f arm64/appcast.xml
wrangler r2 object put orbstack-updates/beta/amd64/appcast.xml -f amd64/appcast.xml

popd
