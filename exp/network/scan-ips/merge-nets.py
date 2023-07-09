netcounts = {}
with open('combined.csv', 'r') as f:
    for l in f.read().split('\n'):
        if l:
            net, count = l.split(',')
            count = int(count)
            netcounts[net] = count

def do_with_base(base_addr, min_val):
    for i in range(2, 254, 2):
        # just a feeling: >100 is less common
        if i <= min_val:
            continue
        tgt0 = f'{base_addr}.{i}.'
        tgt1 = f'{base_addr}.{i+1}.'
        # print(f'{tgt0},{netcounts[tgt0] + netcounts[tgt1]}, (first) {netcounts[tgt0]}, (second) {netcounts[tgt1]}')
        print(f'{tgt0},{.6 * (netcounts[tgt0]) + .4 * (netcounts[tgt0] + netcounts[tgt1])}, (first) {netcounts[tgt0]}, (second) {netcounts[tgt1]}')

do_with_base('192.168', 100)
do_with_base('10', 100)
