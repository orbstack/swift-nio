#!/usr/bin/env bash

set -eo pipefail

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

curl -L https://nixos.org/channels/nixos-unstable/nixexprs.tar.xz | tar -C "$tmpdir" -xJvf - --wildcards '*/programs.sqlite' >&2

# convert CNF database
sqlite3 -csv "$tmpdir"/*/programs.sqlite "select name, package from Programs where system = '$(uname -m)-linux';"
