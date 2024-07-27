#!/usr/bin/env python3

with open('/sys/kernel/debug/page_owner', 'rb', buffering=0) as f:
    while True:
        page = f.read(4096).decode('utf-8')
        if not page:
            break
        lines = page.splitlines()
        l0 = lines[0].split()
        order = int(l0[4][:-1])
        if order >= 2:
            continue
        gfps = l0[6].split('(')[1].strip(')').split('|')

        l1 = lines[1].split()
        flags = l1[9].split('(')[1].strip(')').split('|')

        # skip file-backed
        if 'lru' in flags and 'swapbacked' not in flags:
            continue
        if 'mappedtodisk' in flags:
            continue
        # skip anon
        if 'swapbacked' in flags:
            continue
        # skip page_ext
        if 'page_ext_init' in page:
            continue
        # skip page tables
        if 'pmd_alloc' in page or 'pte_alloc' in page:
            continue

        print(f'order: {order}, flags: {flags}')
        print(page)
        print()

