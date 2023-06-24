#!/bin/bash

set -euo pipefail

from_pkg="$1"
to_exe="$2"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

7z.7zip x -y -o"$tmpdir" "$from_pkg"
7z.7zip x -y -o"$tmpdir" "$tmpdir/Payload~"
from_exe="$tmpdir/Library/Apple/usr/libexec/oah/RosettaLinux/rosetta"

file_fp="$(cat header "$from_exe" | b3sum --no-names)"

if [[ "$from_exe" == "$to_exe" ]]; then
    touch "/out/$file_fp"
    exit
fi

bsdiff "$from_exe" "$to_exe" "/out/$file_fp"
