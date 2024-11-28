#!/usr/bin/env bash

set -eufo pipefail

if [[ "$#" -ne 1 ]]; then
	echo "Usage: $0 <base-ref>"
	exit 1
fi

BASE="$1"

git rebase "$BASE" --exec 'treefmt && git add . && git commit --amend --no-edit'
