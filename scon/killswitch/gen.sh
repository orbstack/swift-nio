#!/usr/bin/env bash

set -euo pipefail

KILLSWITCH_EXPIRE_DAYS=30

echo '//go:build release' > ksconst_release.go
echo '' >> ksconst_release.go
echo 'package killswitch' >> ksconst_release.go
echo '' >> ksconst_release.go
# 30 days from now
ts=$(($(date +%s)+$KILLSWITCH_EXPIRE_DAYS*24*60*60))
echo $ts
echo "const killswitchTimestamp = $ts" >> ksconst_release.go
