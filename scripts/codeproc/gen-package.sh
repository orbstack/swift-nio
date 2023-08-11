#!/usr/bin/env bash

set -xeuo pipefail

TAG="${1:-HEAD}"

# repo root
cd "$(dirname "$0")/../.."
REPO_ROOT="$(pwd)"

tmpdir="$(mktemp -d)"
trap 'rm -fr "$tmpdir"' EXIT

mkdir -p "$tmpdir/repo"
git archive "$TAG" | tar -x -C "$tmpdir/repo"
pushd "$tmpdir/repo"

# remove basic files
rm -fr exp .github keys cli-bin Frameworks
fd -u0 '\.(?:vscode|idea|fleet)' | xargs -0 rm -fr
fd -0 README.md | xargs -0 rm -f

# remove self
rm -fr scripts/codeproc

# remove Go tests
fd -0 '_test\.go$' | xargs -0 rm -f

# remove Swift tests
rm -fr swift/*/Tests

# vendor gvisor
rm -fr vendor/gvisor # is a symlink
git clone git@github.com:orbstack/gvisor-macvirt --reference-if-able ~/code/vm/gvisor --depth 1 vendor/gvisor
rm -fr vendor/gvisor/.git

# filter source code last, so we include everything
pushd "$REPO_ROOT/scripts/codeproc"
pnpm install
node index.js "$tmpdir/repo"
popd

# vendor kernel
# causes problems with case-insensitive apfs
# git clone git@github.com:orbstack/linux-macvirt-priv --reference-if-able ~/code/projects/orbstack/linux-orbstack --depth 1 vendor/linux
# rm -fr vendor/linux/.git
# rm -f vendor/linux/configs/debug

# replace bundle ID
find . -type f -print0  | xargs -0 gsed -i 's/dev.kdrag0n.MacVirt/com.anthropic.OrbStackInternal/g'
# TODO: team ID?
# from HUAQ24HBR6

# replace sentry org ID to look better
gsed -i 's/kdrag0n/orbital-labs/g' config.sh

# temp: copy latest readme
cp -f "$REPO_ROOT/README.dist.md" .

# push 
git init
git add .
git commit -am "External release $TAG"
git checkout -b main
git remote add origin git@github.com:orbstack/orbstack-ext-main
git push -f origin main
