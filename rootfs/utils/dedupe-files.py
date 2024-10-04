import collections
import os
import sys

hashes = collections.defaultdict(list)

with open(sys.argv[1], "r") as f:
    for l in f.read().splitlines():
        hash, path = l.split(maxsplit=2)
        hashes[hash].append(path)

for hash, paths in hashes.items():
    if len(paths) > 1:
        # prefer uid/gid/mode from */nix/* copy, if any
        for src_path in paths:
            if "/nix/" in src_path:
                break
        else:
            src_path = paths[0]

        # delete the rest, and make hard links
        for p in paths:
            if p == src_path:
                continue
            os.unlink(p)
            os.link(src_path, p)
