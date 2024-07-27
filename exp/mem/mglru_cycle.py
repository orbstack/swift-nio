#!/usr/bin/env python3

import mglru_inc
import mglru_reclaim
import kpagemap
import time

mglru_inc.DEBUG = False
mglru_reclaim.DEBUG = False

while True:
    print('----------- AGE + RECLAIM -------------')

    print('BEFORE:')
    _, _, totals, xtotals = kpagemap.scan()
    print('    raw free:', totals['free'] / 1024 / 1024, 'MiB')
    print('    CONTIG free:', xtotals['free'] / 1024 / 1024, 'MiB')
    print('        raw anon:', totals['anon'] / 1024 / 1024, 'MiB')
    print('        raw slab:', totals['slab'] / 1024 / 1024, 'MiB')
    print('        raw file:', totals['file'] / 1024 / 1024, 'MiB')
    print('        CONTIG file:', xtotals['file'] / 1024 / 1024, 'MiB')

    # reclaim the last gen FIRST, then age
    # this allows us to keep all 4 gens full, rather than effectively limiting it to 3 gens (if oldest gen is always empty of file pages)
    mglru_reclaim.main()
    mglru_inc.main()

    print('AFTER:')
    _, _, totals, xtotals = kpagemap.scan()
    print('    raw free:', totals['free'] / 1024 / 1024, 'MiB')
    print('    CONTIG free:', xtotals['free'] / 1024 / 1024, 'MiB')
    print('        raw anon:', totals['anon'] / 1024 / 1024, 'MiB')
    print('        raw slab:', totals['slab'] / 1024 / 1024, 'MiB')
    print('        raw file:', totals['file'] / 1024 / 1024, 'MiB')
    print('        CONTIG file:', xtotals['file'] / 1024 / 1024, 'MiB')

    print()
    print()
    time.sleep(15)
