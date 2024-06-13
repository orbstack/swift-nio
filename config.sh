# Code signing
# signing ID must be changed in other places too
VMGR_SIGNING_ID="dev.kdrag0n.MacVirt.vmgr"
SIGNING_CERT="Developer ID Application: Orbital Labs, LLC (U.S.) (HUAQ24HBR6)"
SIGNING_CERT_DEV="Apple Development: Danny Lin (A2LS84RQFY)"

# Keychain profile for notary submissions
# To create/log in: xcrun notarytool store-credentials
NOTARY_KEYCHAIN_PROFILE=main


#
# Updates
# (unused if you don't need auto-update)
#

# Sparkle CLI tools
SPARKLE_BIN=~/Library/Developer/Xcode/DerivedData/MacVirt-cvlazugpvgfgozfesiozsrqnzfat/SourcePackages/artifacts/sparkle/Sparkle/bin

# for uploading debug symbols (login required too)
SENTRY_ORG=kdrag0n
SENTRY_PROJECT=orbstack

CDN_BASE_URL=https://cdn-updates.orbstack.dev

_SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )
source "$_SCRIPT_DIR/config.local.sh" || :
unset _SCRIPT_DIR
