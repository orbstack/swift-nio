#!/usr/bin/env python3

import sys

# 256M is optimal for chunk size:
# 1M: 176543/3549857 requests required mapping: 4.97%
# 8M: 161394/3549857 requests required mapping: 4.55%
# 64M: 119914/3549857 requests required mapping: 3.38%
# 128M: 111368/3549857 requests required mapping: 3.14%
# 256M: 109780/3549857 requests required mapping: 3.09%
# 1G: 199601/3549857 requests required mapping: 5.62%

MAP_LIMIT = 8 * 1024 * 1024 * 1024 # 8 GiB
MAP_CHUNK_SIZE = 256 * 1024 * 1024 # 256 MiB
MAP_CHUNKS = MAP_LIMIT // MAP_CHUNK_SIZE

PAGE_SIZE = 16384

mapped_intervals = []
total_mapped = 0

total_reqs = 0
reqs_mapped = 0

with open(sys.argv[1], 'r') as f:
    for line in f.read().splitlines():
        parts = line.split(',')
        if len(parts) == 3:
            typ = int(parts[0])
            offset = int(parts[1])
            length = int(parts[2])

            # attempt to map and read this part
            start_page_i = offset // PAGE_SIZE
            end_page_i = (offset + length) // PAGE_SIZE

            had_to_map = False
            for page_i in range(start_page_i, end_page_i):
                addr_start = page_i * PAGE_SIZE
                # is there an interval that contains this?
                if any(addr_start >= start and addr_start < end for start, end in mapped_intervals):
                    # yes
                    pass
                else:
                    # need to map it

                    # evict one if needed
                    if total_mapped >= MAP_LIMIT:
                        # evict the first one
                        del mapped_intervals[0]
                        total_mapped -= MAP_CHUNK_SIZE

                    new_mapping = (addr_start, addr_start + MAP_CHUNK_SIZE)
                    mapped_intervals.append(new_mapping)
                    total_mapped += MAP_CHUNK_SIZE
                    had_to_map = True
            
            total_reqs += 1
            if had_to_map:
                reqs_mapped += 1
            
print(f'{reqs_mapped}/{total_reqs} requests required mapping: {reqs_mapped / total_reqs * 100:.2f}%')
