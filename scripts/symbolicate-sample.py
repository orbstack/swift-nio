#!/usr/bin/env python3

import argparse
import subprocess
import collections

parser = argparse.ArgumentParser(description="Symbolicate a sample file")
parser.add_argument("sample_file", help="Path to the sample file")
parser.add_argument("dsym", help="Path to the dsym directory")
args = parser.parse_args()

with open(args.sample_file, "r") as f, open(
    args.sample_file + ".symbolicated", "w"
) as out_f:
    # collect all addrs
    all_addrs = collections.defaultdict(set)
    for line in f:
        if "???" in line and "load address" in line:
            load_address = line[
                line.index("load address") + len("load address") :
            ].strip()
            base_addr = load_address[: load_address.index(" ")]
            end_addr = load_address[
                load_address.index("[") + 1 : load_address.index("]")
            ]

            all_addrs[base_addr].add(end_addr)
        out_f.write(line)

    # run atos on everything
    all_addr_mappings = {}
    for base_addr, end_addrs in all_addrs.items():
        end_addrs_list = list(end_addrs)
        output = subprocess.check_output(
            ["atos", "-i", "-o", args.dsym, "-l", base_addr, *end_addrs]
        )
        for i, lookups in enumerate(output.decode().strip().split("\n\n")):
            symbols = lookups.split("\n")[::-1]
            all_addr_mappings[(base_addr, end_addrs_list[i])] = symbols

    f.seek(0)
    for line in f:
        if "???" in line and "load address" in line:
            load_address = line[
                line.index("load address") + len("load address") :
            ].strip()
            base_addr = load_address[: load_address.index(" ")]
            end_addr = load_address[
                load_address.index("[") + 1 : load_address.index("]")
            ]

            symbols = all_addr_mappings[(base_addr, end_addr)]
            indent_level = line.index("???")
            symbols_str = ("\n" + " " * indent_level).join(symbols)
            line = line[:indent_level] + symbols_str + line[line.index("  [") :]
        out_f.write(line)
        print(line, end="")
