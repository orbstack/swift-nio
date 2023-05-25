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

VMGR_BIN="OrbStack Helper (VM)"

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

    # build swift lib
    pushd swift
    make lib-release
    popd

    OUT=./

    pushd macvmgr
    rm -f $OUT/"$VMGR_BIN"
    go generate ./conf/appver ./drm/killswitch
    BUILD_TYPE=release EXTRA_LDFLAGS="-s -w" ./build.sh -tags release -trimpath -o $OUT/"$VMGR_BIN"
    codesign -f --timestamp --options=runtime --entitlements vmgr.entitlements -i dev.kdrag0n.MacVirt -s ECD9A0D787DFCCDD0DB5FF21CD2F6666B9B5ADC2 $OUT/"$VMGR_BIN"
    popd

    pushd scon
    rm -f $OUT/scon
    go build -tags release -trimpath -ldflags="-s -w" -o $OUT/scli ./cmd/scli
    codesign -f --timestamp --options=runtime -i dev.kdrag0n.MacVirt.scli -s ECD9A0D787DFCCDD0DB5FF21CD2F6666B9B5ADC2 $OUT/scli
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

    rm -fr out/$arch_go
    mkdir -p out/$arch_go
    mv build/$arch_go/OrbStack.app out/$arch_go/
    
    mkdir -p out/$arch_go/dsym
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
    create-dmg --overwrite --dmg-title="Install OrbStack $SHORT_VER" $arch/OrbStack.app $arch

    if $NOTARIZE; then
        # notarize
        xcrun notarytool submit $arch/*.dmg --keychain-profile main --wait

        # staple
        xcrun stapler staple $arch/*.dmg
    fi

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
    ./publish-update.sh "${built_dmgs[@]}"
fi
