#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
GOARCH=amd64 CGO_ENABLED=1 go build
codesign --entitlements vmm.entitlements -s - macvmgr || :
