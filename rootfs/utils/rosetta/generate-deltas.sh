#!/bin/bash

set -euo pipefail
mkdir -p cache

curl -L https://swscan.apple.com/content/catalogs/others/index-rosettaupdateauto-1.sucatalog.gz | gunzip | python3 parse-catalog.py > catalog

# download
target_pkg="$(./download-one.sh "$(cat target)")"
cat catalog | xargs -P 8 -n 1 ./download-one.sh > src_pkgs

# extract target
7z.7zip x -y "cache/$target_pkg"
7z.7zip x -y "Payload~"
target_exe="Library/Apple/usr/libexec/oah/RosettaLinux/rosetta"

echo -n 'orbrosettafp' > header
mkdir -p /out

for from_pkg in $(cat src_pkgs); do
    ./generate-one.sh "cache/$from_pkg" "$target_exe"
done

target_fp="$(cat header "$target_exe" | b3sum --no-names)"
touch "/out/$target_fp"
