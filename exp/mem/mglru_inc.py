DEBUG = True

def main():
    with open('/sys/kernel/debug/lru_gen', 'r') as f:
        data = f.read()

    cur_memcg_id = None
    max_gen = None
    for l in data.splitlines():
        if l.startswith('memcg'):
            if cur_memcg_id is not None and cur_memcg_id != '0':
                if DEBUG:
                    print(f'AGE memcg {cur_memcg_id} gen {max_gen}')
                with open('/sys/kernel/debug/lru_gen', 'w') as wf:
                    # can_swap=0 to skip anon
                    # force_scan=0 to use bloom filter to skip 
                    wf.write(f'+ {cur_memcg_id} 0 {max_gen} 0 0\n')
            cur_memcg_id = l.split()[1]
        elif l.startswith(' node'):
            continue
        else:
            gen, age_ms, nr_anon, nr_file = l.split()
            max_gen = int(gen)

if __name__ == '__main__':
    main()
