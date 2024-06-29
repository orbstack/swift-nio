# Command-not-found GPT

Batch GPT-3.5 inference to select the best package to install in the command-not-found handler.

## Generate/update data

In a Linux machine:

- `./make-batch.sh`
- Upload to https://platform.openai.com/batches/
- Download results
- `./gen-overrides.py batch_*output.jsonl overrides.csv`

**TODO: update automatically.** Can't use GitHub Actions due to runtime limit, unless we do it in 2 stages and poll for completion after 1 day.

Inference costs ~$0.30.
