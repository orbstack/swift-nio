#!/usr/bin/env python3

import argparse
import subprocess
from tqdm import tqdm

parser = argparse.ArgumentParser(description="Symbolicate a sample file")
parser.add_argument("sample_file", help="Path to the sample file")
parser.add_argument("dsym", help="Path to the dsym directory")
args = parser.parse_args()

with open(args.sample_file, "r") as f, open(
    args.sample_file + ".symbolicated", "w"
) as out_f:
    num_lines = sum(1 for _ in f)
    f.seek(0)
    for line in tqdm(f, total=num_lines):
        if "???" in line and "load address" in line:
            load_address = line[
                line.index("load address") + len("load address") :
            ].strip()
            base_addr = load_address[: load_address.index(" ")]
            end_addr = load_address[
                load_address.index("[") + 1 : load_address.index("]")
            ]

            output = subprocess.check_output(
                ["atos", "-o", args.dsym, "-l", base_addr, end_addr]
            )
            symbol = output.decode().strip()
            line = line[: line.index("???")] + symbol + line[line.index("  [") :]
        out_f.write(line)
