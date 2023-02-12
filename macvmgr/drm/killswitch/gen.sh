#!/usr/bin/env bash

set -euo pipefail

echo '//go:build release' > ksconst_release.go
echo '' >> ksconst_release.go
echo 'package killswitch' >> ksconst_release.go
echo '' >> ksconst_release.go
# 21 days from now
ts=$(date -v+21d +%s)
echo "const killswitchTimestamp = $ts" >> ksconst_release.go
