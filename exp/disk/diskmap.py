#!/usr/bin/env python3

import sys
import os
import numpy as np
from PIL import Image

colors = [
    # read
    np.array([0, 0, 255], dtype=np.uint8), # blue
    # write
    np.array([255, 0, 0], dtype=np.uint8), # red
    # discard
    np.array([0, 255, 0], dtype=np.uint8), # green
]

# pixels = np.zeros((32768, 32768, 3), dtype=np.uint8)
pixels = np.full((32768*32768, 3), 255, dtype=np.uint8)

with open(sys.argv[1], 'r') as f:
    for line in f.read().splitlines():
        parts = line.split(',')
        if len(parts) == 3:
            typ = int(parts[0])
            offset = int(parts[1]) // 4096
            length = int(parts[2])

            for i in range(length // 4096):
                if offset + i < 32768*32768:
                    pixels[offset + i] = colors[typ]

img = Image.fromarray(pixels.reshape(32768, 32768, 3))
img.save('diskmap.png')
os.system('open diskmap.png')
