# Measures an exported frame for bw-verify.mjs: mean chroma (max-min per
# pixel, 0 on a truly neutral image), mean per-channel level, and mean luma.
# Prints JSON on stdout.
#
#   python3 scripts/bw-verify.py <image>

import json
import sys

from PIL import Image

img = Image.open(sys.argv[1]).convert("RGB")
px = img.load()
w, h = img.size
# Subsample: a few thousand pixels settle these means well enough, and keeps
# the probe instant on a full-size export.
step = max(1, min(w, h) // 100)
chroma = luma = rs = gs = bs = 0.0
n = 0
for y in range(0, h, step):
    for x in range(0, w, step):
        r, g, b = px[x, y]
        chroma += max(r, g, b) - min(r, g, b)
        luma += 0.299 * r + 0.587 * g + 0.114 * b
        rs += r
        gs += g
        bs += b
        n += 1

print(json.dumps({
    "chroma": chroma / n,
    "luma": luma / n,
    "r": rs / n,
    "g": gs / n,
    "b": bs / n,
    "pixels": n,
}))
