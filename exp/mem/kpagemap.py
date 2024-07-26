#!/usr/bin/env python3

import math
import numpy as np
from PIL import Image
import collections
import os

PAGE_SIZE = os.sysconf('SC_PAGE_SIZE')
NR_CONT_PAGES = 16384 // PAGE_SIZE

mem_types = {
    'free': np.array([255, 255, 255], dtype=np.uint8), # white
    'file': np.array([255, 0, 0], dtype=np.uint8), # red
    'anon': np.array([0, 255, 0], dtype=np.uint8), # green
    #'misc_ram': np.array([255, 255, 0], dtype=np.uint8), # yellow
    'slab': np.array([0, 0, 255], dtype=np.uint8), # blue
    'unknown': np.array([255, 0, 255], dtype=np.uint8), # magenta
}

pixels = []
pixels_str = []

totals = collections.defaultdict(int)
totals_with_order = collections.defaultdict(lambda: collections.defaultdict(int))


#  0. LOCKED
#  1. ERROR
#  2. REFERENCED
#  3. UPTODATE
#  4. DIRTY
#  5. LRU
#  6. ACTIVE
#  7. SLAB
#  8. WRITEBACK
#  9. RECLAIM
# 10. BUDDY
# 11. MMAP
# 12. ANON
# 13. SWAPCACHE
# 14. SWAPBACKED
# 15. COMPOUND_HEAD
# 16. COMPOUND_TAIL
# 17. HUGE
# 18. UNEVICTABLE
# 19. HWPOISON
# 20. NOPAGE
# 21. KSM
# 22. THP
# 23. BALLOON
# 24. ZERO_PAGE
# 25. IDLE
flag_names = ['LOCKED', 'ERROR', 'REFERENCED', 'UPTODATE', 'DIRTY', 'LRU', 'ACTIVE', 'SLAB', 'WRITEBACK', 'RECLAIM', 'BUDDY', 'MMAP', 'ANON', 'SWAPCACHE', 'SWAPBACKED', 'COMPOUND_HEAD', 'COMPOUND_TAIL', 'HUGE', 'UNEVICTABLE', 'HWPOISON', 'NOPAGE', 'KSM', 'THP', 'BALLOON', 'ZERO_PAGE', 'IDLE']
def join_flags(flags):
    return ','.join([flag_names[i] for i in range(len(flag_names)) if flags & (1 << i)])

last_compound_type = None
compound_i = None
with open('/proc/kpageflags', 'rb') as f:
    while True:
        # read 8 bytes
        flags = f.read(8)
        if not flags:
            break

        # determine type
        flags = int.from_bytes(flags, 'little')
        if flags & (1 << 16): # COMPOUND_TAIL
            mem_type = last_compound_type
            compound_count += 1
        if flags & (1 << 10): # BUDDY
            mem_type = 'free'
        elif flags & (1 << 7): # SLAB
            mem_type = 'slab'
        elif flags & (1 << 14) or flags & (1 << 12): # SWAPBACKED or ANON
            mem_type = 'anon'
        elif flags & (1 << 5): # LRU (and not ANON)
            mem_type = 'file'
        elif flags & (1 << 20): # NOPAGE
            continue
        else:
            #print(f'unknown flags: {join_flags(flags)}')
            mem_type = 'unknown'

        # save last compound head's flags
        if flags & (1 << 15): # COMPOUND_HEAD
            last_compound_type = mem_type
            compound_count = 1
        # elif flags & (1 << 16): # COMPOUND_TAIL

        pixels.append(mem_types[mem_type])
        pixels_str.append(mem_type)
        totals[mem_type] += PAGE_SIZE

for mem_type, total in totals.items():
    print(f'{mem_type}: {total / 1024 / 1024} MiB')

print('---')
print('total:', sum(totals.values()) / 1024 / 1024, 'MiB')

# scan for contigs
print()
print()
xtotals = collections.defaultdict(int)
for page_i in range(0, len(pixels_str), NR_CONT_PAGES):
    pixels_str_slice = pixels_str[page_i:page_i + NR_CONT_PAGES]
    if all(p == 'free' for p in pixels_str_slice):
        xtotals['free'] += NR_CONT_PAGES * PAGE_SIZE
    if all(p == 'file' for p in pixels_str_slice):
        xtotals['file'] += NR_CONT_PAGES * PAGE_SIZE
    if all(p == 'free' or p == 'file' for p in pixels_str_slice):
        xtotals['free | file'] += NR_CONT_PAGES * PAGE_SIZE
xtotals['free + file'] = xtotals['free'] + xtotals['file']
xtotals['non-contig free + file'] = totals['free'] + totals['file']

print('CONTIGUOUS 16K (FREEABLE):')
for mem_type, total in xtotals.items():
    print(f'{mem_type}: {total / 1024 / 1024} MiB')

# make image
width = int(math.ceil(math.sqrt(len(pixels))))
height = int(math.ceil(len(pixels) / width))
padding = width * height - len(pixels)
pixels.extend([mem_types['free']] * padding)
img = Image.fromarray(np.array(pixels).reshape((height, width, 3)))
img.save('kpageflags.png')

# open image
os.system('/opt/orbstack-guest/bin-hiprio/open kpageflags.png')
