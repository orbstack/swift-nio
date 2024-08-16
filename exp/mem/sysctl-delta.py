import subprocess
import sys
import time

def parse_sysctl(output):
    ret = {}
    for l in output.split('\n'):
        if 'reusable' in l:
            k,v = l.split(': ')
            ret[k] = int(v)
    return ret

while True:
    out_before = parse_sysctl(subprocess.run(['sysctl', '-a'], capture_output=True, text=True, check=True).stdout)
    time.sleep(1)
    out_after = parse_sysctl(subprocess.run(['sysctl', '-a'], capture_output=True, text=True, check=True).stdout)

    for k in out_before.keys():
        if k in out_after:
            if out_before[k] != out_after[k]:
                print(f'{k}: +{out_after[k] - out_before[k]}')
    print()
