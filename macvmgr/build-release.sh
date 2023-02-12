#!/usr/bin/env bash

set -euxo pipefail

ARCH="${1:-arm64}"

cd "$(dirname "$0")"

export GOARCH=$ARCH
export CGO_ENABLED=1

OUT=../dist

rm -fr $OUT
mkdir -p $OUT/bin $OUT/assets/release

go build -tags release -trimpath -ldflags="-s -w" -o $OUT/bin/macvmgr
codesign --timestamp --options=runtime --entitlements vmm.entitlements -s ECD9A0D787DFCCDD0DB5FF21CD2F6666B9B5ADC2 $OUT/bin/macvmgr

pushd ../scon
go build -tags release -trimpath -ldflags="-s -w" -o $OUT/bin/orbctl ./cmd/scli
codesign --timestamp --options=runtime -s ECD9A0D787DFCCDD0DB5FF21CD2F6666B9B5ADC2 $OUT/bin/orbctl
popd

pushd $OUT/bin
ln -sf orbctl orb
ln -sf orbctl lnxctl
ln -sf orbctl lnx
popd

cp -rc ../assets/release/$ARCH $OUT/assets/release


# get it notarized
pushd $OUT
zip -r notary.zip bin
xcrun notarytool submit notary.zip --keychain-profile main --wait
rm -f notary.zip
popd

# zip it
pushd $OUT
rm -f ../orbstack-$ARCH-dist.zip
zip -r ../orbstack-$ARCH-dist.zip *
popd

# // Staple the package.
# xcrun stapler staple niimath.pkg
# // Optionally make a zip for the package.
# ditto -c -k --sequesterRsrc niimath.pkg niimath.zip 
