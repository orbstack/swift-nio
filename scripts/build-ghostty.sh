#!/usr/bin/env bash

GHOSTTY_TAG=orbstack-1
set -eufo pipefail
cd "$(dirname "$0")"

echo "Cloning ghostty"

cd "../vendor"
pushd .
if [[ ! -d "ghostty" ]]; then
  git clone -b $GHOSTTY_TAG --depth=1 git@github.com:orbstack/ghostty
else
  (cd ghostty; git fetch && git checkout $GHOSTTY_TAG && git pull)
fi
popd

echo "Building ghostty"

cd ghostty
zig build -Dversion-string=0.0.0-orbstack -Demit-macos-app=false  
