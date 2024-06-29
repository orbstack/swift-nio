#!/usr/bin/env python3

import collections
import csv
import sys

packages_by_cmd = collections.defaultdict(list)

with open(sys.argv[1], 'r') as f:
    reader = csv.reader(f)
    for cmd, package in reader:
        # exclude shell scripts
        if cmd.endswith('.sh'):
            continue

        packages_by_cmd[cmd].append(package)

with open(sys.argv[2], 'r') as f:
    reader = csv.reader(f)
    for cmd, package in reader:
        packages_by_cmd[cmd] = [package]

with open(sys.argv[3], 'w+') as f:
    writer = csv.writer(f, lineterminator='\n')
    for cmd, packages in packages_by_cmd.items():
        for package in packages:
            writer.writerow([cmd, package])
