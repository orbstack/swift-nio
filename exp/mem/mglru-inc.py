
with open('/sys/kernel/debug/lru_gen', 'r') as f:
    data = f.read()

cur_memcg_id = None
max_gen = None
for l in data.splitlines():
    if l.startswith('memcg'):
        if cur_memcg_id is not None and cur_memcg_id != '0':
            print(f'+ {cur_memcg_id} 0 {max_gen}')
            with open('/sys/kernel/debug/lru_gen', 'w') as wf:
                wf.write(f'+ {cur_memcg_id} 0 {max_gen}\n')
        cur_memcg_id = l.split()[1]
    elif l.startswith(' node'):
        continue
    else:
        gen, age_ms, nr_anon, nr_file = l.split()
        max_gen = int(gen)
