#!/usr/bin/env python3

import math
import numpy as np
from PIL import Image
import collections
import os

GENERATE_IMAGE = True

BLOCK_SIZE = 4096

COLORS = [
    np.array([255, 0, 0], dtype=np.uint8), # red
    np.array([0, 0, 255], dtype=np.uint8), # blue
]

entries = []
with open('apfsmap.csv', 'r') as f:
    for line in f.read().split('\n'):
        if not line:
            continue
        file_off, config_bytes = line.split(',')
        file_off = int(file_off)
        config_bytes = int(config_bytes)
        entries.append((file_off, config_bytes))

last_file_off, last_config_bytes = entries[-2]
file_end = last_file_off + last_config_bytes
num_pixels = file_end // BLOCK_SIZE
pixels = [np.array([255, 255, 255], dtype=np.uint8)] * num_pixels

print('# pixels:', len(pixels))
for i, (file_off, config_bytes) in enumerate(entries):
    for file_pos in range(file_off, file_off + config_bytes, BLOCK_SIZE):
        try:
            pixels[file_pos // BLOCK_SIZE] = COLORS[i % len(COLORS)]
        except IndexError:
            # ignore last few blocks (backup GPT)
            print(f'IndexError: {file_pos // BLOCK_SIZE}')

# make image
width = int(math.ceil(math.sqrt(len(pixels))))
height = int(math.ceil(len(pixels) / width))
padding = width * height - len(pixels)
pixels.extend([np.array([255, 255, 255], dtype=np.uint8)] * padding)
img = Image.fromarray(np.array(pixels).reshape((height, width, 3)))
img.save('apfsmap.png')

# open image
os.system('open apfsmap.png')
