#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
go build "$@"
codesign --entitlements vmm.entitlements -s - macvmgr || :
