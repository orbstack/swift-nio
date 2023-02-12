#!/usr/bin/env bash

set -euo pipefail

git describe --tags --always --dirty > version.txt
git rev-parse HEAD >> version.txt
