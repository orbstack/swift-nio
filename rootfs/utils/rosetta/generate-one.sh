#!/bin/bash

set -euo pipefail

from_pkg="$1"
to_exe="$2"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

7z x -y -o"$tmpdir" "$from_pkg"
7z x -y -o"$tmpdir" "$tmpdir/Payload~"
from_exe="$tmpdir/Library/Apple/usr/libexec/oah/RosettaLinux/rosetta"

file_fp="$(cat header "$from_exe" | b3sum --no-names)"

# most fingerprints are duplicates
# if it already exists, don't bother to recalculate
if [[ -e "/out/$file_fp" ]]; then
    exit
fi

if [[ "$from_exe" == "$to_exe" ]]; then
    touch "/out/$file_fp"
    exit
fi

# to prevent race when running in parallel, work with a temp file, then move it to dest
tmp_out="$tmpdir/outf"

bsdiff "$from_exe" "$to_exe" "$tmp_out"
# encrypt
/work/b3enc "$from_exe" "$tmp_out" "$tmp_out"

mv "$tmp_out" "/out/$file_fp"
