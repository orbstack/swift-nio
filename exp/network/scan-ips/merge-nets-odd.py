netcounts = {}
with open('combined.csv', 'r') as f:
    for l in f.read().split('\n'):
        if l:
            net, count = l.split(',')
            count = int(count)
            netcounts[net] = count

def do_with_base(base_addr, min_val):
    entries = []
    pairs = {}
    for i in range(1, 254, 1):
        # just a feeling: >100 is less common
        if i <= min_val:
            continue
        if i % 2 == 0:
            base = f'{base_addr}.{i}.'
            other = f'{base_addr}.{i+1}.'
        else:
            base = f'{base_addr}.{i}.'
            other = f'{base_addr}.{i-1}.'
        base_count = netcounts[base]
        other_count = netcounts[other]
        score = .6 * (base_count) + .4 * (base_count + other_count)
        #print(f'{base},{score}, (first) {base_count}, (second) {other_count}')

        pairs[base] = other
        pairs[other] = base
        entries.append((base, score, base_count, other_count))
    
    # sort ascending
    entries.sort(key=lambda x: x[1])

    # merge: top entry first, discard pair
    pairs_done = set()
    merged_entries = []
    for entry in entries:
        if entry[0] in pairs_done:
            continue
        pairs_done.add(entry[0])
        pairs_done.add(pairs[entry[0]])
        merged_entries.append(entry)
    
    # print 
    for (base, score, base_count, other_count) in merged_entries:
        print(f'{base},{score}, (first) {base_count}, (second) {other_count}')

do_with_base('192.168', 100)
do_with_base('10', 100)
