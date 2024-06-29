#!/usr/bin/env bash

set -eufo pipefail

./download-db.sh > db.csv
./gen-batch.py db.csv
echo "Done! Upload batch.jsonl to https://platform.openai.com/batches/"
