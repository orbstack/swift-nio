#!/bin/bash

set -euo pipefail

mkdir -p cache
mkdir -p /out

curl -L https://swscan.apple.com/content/catalogs/others/index-rosettaupdateauto-1.sucatalog.gz | gunzip | python3.11 parse-catalog.py > catalog

# download
target_pkg="$(./download-one.sh "$(cat target)")"
cat catalog | xargs -P 8 -n 1 ./download-one.sh > src_pkgs

# extract target
7z x -y "cache/$target_pkg"
7z x -y "Payload~"
target_exe="Library/Apple/usr/libexec/oah/RosettaLinux/rosetta"

echo -ne 'orb\x00rosetta\x00fp' > header

cat src_pkgs | parallel "./generate-one.sh 'cache/{}' '$target_exe'"

target_fp="$(cat header "$target_exe" | b3sum --no-names)"
touch "/out/$target_fp"
