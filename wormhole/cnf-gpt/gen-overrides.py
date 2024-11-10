#!/usr/bin/env python3

import json
import csv
import sys

with open(sys.argv[1], "r") as f:
    with open(sys.argv[2], "w+") as wf:
        writer = csv.writer(wf, lineterminator="\n")
        for line in f.read().split("\n"):
            if line:
                resp = json.loads(line)
                cmd = resp["custom_id"]
                func_args = json.loads(
                    resp["response"]["body"]["choices"][0]["message"]["tool_calls"][0][
                        "function"
                    ]["arguments"]
                )
                pkg = func_args["package"]

                # overrides for the overrides:
                # older kernel versions are deprecated
                if pkg.startswith("linuxKernel.packages."):
                    pkg = f"linuxKernel.packagesAliases.linux_latest.{pkg.split('.')[-1]}"

                writer.writerow([cmd, pkg])
