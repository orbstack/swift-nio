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

    popd
}

rm -fr swift/build

# builds can't be parallel
build_one arm64 arm64
#build_one amd64 x86_64

function package_one() {
    local arch="$1"

    # dmg
    mkdir -p dmg/$arch
    create-dmg --overwrite $arch/OrbStack.app dmg/$arch

    # notarize
    #xcrun notarytool submit dmg/$arch/*.dmg --keychain-profile main --wait

    # staple
    #xcrun stapler staple dmg/$arch/*.dmg

    name="$(basename dmg/$arch/*.dmg .dmg)"
    mv dmg/$arch/*.dmg "dmg/$name $arch.dmg"
    rm -fr dmg/$arch
}

pushd swift/build

package_one arm64 &
#package_one amd64 &
wait

popd
