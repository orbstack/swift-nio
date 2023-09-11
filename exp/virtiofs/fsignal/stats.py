import statistics

import sys

ds = []
with open(sys.argv[1], 'r') as f:
    for l in f.read().split('\n'):
        if l:
            ds.append(int(l) / 1000)

print('avg ', int(statistics.mean(ds)))
print('median ', int(statistics.median(ds)))
print('stdev ', int(statistics.stdev(ds)))
print('min ', int(min(ds)))
print('max ', int(max(ds)))
print('count ', len(ds))
print()

# ascii histogram
BUCKET_SIZE = 5
buckets = [0] * 65536
for d in ds:
    bucket = int(d // BUCKET_SIZE)
    if bucket > 65535:
        bucket = 65535
    buckets[bucket] += 1

for i in range(0, 65536):
    if buckets[i] > 1:
        print(f'{i*BUCKET_SIZE}-{(i+1)*BUCKET_SIZE}: {buckets[i]}')

# # make a histogram
# import matplotlib.pyplot as plt
# import numpy as np

# plt.hist(ds, bins=100)
# plt.show()
