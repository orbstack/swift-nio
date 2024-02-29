#!/usr/bin/env bash

set -euo pipefail

repos="$(cat /build/os/zsh/repos.list)"

# split by slash and clone into repos for
# path format: $ZDOTDIR/cache/https-COLON--SLASH--SLASH-github.com-SLASH-$user-SLASH-$repo
mkdir -p cache
for repo in $repos; do
    user=$(echo $repo | cut -d'/' -f1)
    repo=$(echo $repo | cut -d'/' -f2)
    path=cache/https-COLON--SLASH--SLASH-github.com-SLASH-$user-SLASH-$repo
    git clone --depth 1 https://github.com/$user/$repo $path
    rm -rf $path/.git
done

# recursively delete .gif, .svg files
find cache -name '*.gif' -o -name '*.svg' -exec rm -f {} \;
