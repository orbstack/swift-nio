DEBUG = True

def main():
    with open('/sys/kernel/debug/lru_gen', 'r') as f:
        data = f.read()

    cur_memcg_id = None
    min_gen = None
    nr_gens = 0
    for l in data.splitlines():
        if l.startswith('memcg'):
            if cur_memcg_id is not None and cur_memcg_id != '0' and nr_gens > 2:
                if DEBUG:
                    print(f'RECLAIM memcg {cur_memcg_id} gen {min_gen}')
                with open('/sys/kernel/debug/lru_gen', 'w') as wf:
                    # swappiness 0 = file only
                    wf.write(f'- {cur_memcg_id} 0 {min_gen} 0\n')
            cur_memcg_id = l.split()[1]
            min_gen = -1
            nr_gens = 0
        elif l.startswith(' node'):
            continue
        else:
            gen, age_ms, nr_anon, nr_file = l.split()
            if min_gen == -1:
                min_gen = int(gen)
            nr_gens += 1

if __name__ == '__main__':
    main()
