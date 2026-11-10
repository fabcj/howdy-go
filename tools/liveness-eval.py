# Measures how much a face region changes between an IR emitter's lit frame
# and the off-phase frame before it, on sessions recorded with
# `face-capture -keep-dark`. A real face is lit by the emitter and goes dark
# when it's off, while a screen glows the same in both and a print reflects
# whatever light there is. Whether that separates the user from attacks on
# your camera is what this reports; it decides nothing, and howdy-go has no
# threshold for it until recordings of real attacks back one.
#
#   python3 -I tools/liveness-eval.py [--faces DIR] [--models DIR] [--score 0.6]
#
# Per label: the face's mean brightness in the off frame over the lit frame
# (0 = vanishes when the emitter is off, 1 = unchanged), and the same for
# everything outside the face. Labels starting with attack- are photos or
# screens. Prints numbers only, never frames.
import argparse
import glob
import json
import os
import statistics
import sys

import cv2
import numpy as np

cv2.setNumThreads(1)

cache = os.environ.get("XDG_CACHE_HOME") or os.path.join(os.path.expanduser("~"), ".cache")
p = argparse.ArgumentParser()
p.add_argument("--faces", default=os.path.join(cache, "howdy-go", "faces"))
p.add_argument("--models", default=os.path.join(cache, "howdy-go", "models"))
p.add_argument("--score", type=float, default=0.6, help="YuNet score for finding the face in the lit frame")
p.add_argument("--min-pairs", type=int, default=10, help="fewest pairs a label needs to be summarised")
args = p.parse_args()

yunet = os.path.join(args.models, "yunet_2026may.onnx")
if not os.path.exists(yunet):
	sys.exit("liveness-eval: %s is missing, run tools/fetch-face-models" % yunet)
det = cv2.FaceDetectorYN.create(yunet, "", (320, 320), args.score, 0.3, 5000)


def read(path):
	return cv2.imread(path, cv2.IMREAD_GRAYSCALE)


def face_box(lit):
	"""The best face in the lit frame as x0, y0, x1, y1, or None."""
	h, w = lit.shape
	det.setInputSize((w, h))
	clahe = cv2.createCLAHE(clipLimit=2.0, tileGridSize=(8, 8)).apply(lit)
	_, rows = det.detect(cv2.cvtColor(clahe, cv2.COLOR_GRAY2BGR))
	if rows is None:
		return None
	x, y, bw, bh = rows[0][:4]
	return int(x), int(y), int(x + bw), int(y + bh)


def ratios(lit, dark):
	"""(face ratio, background ratio), or None when there's no usable face."""
	box = face_box(lit)
	if box is None:
		return None
	h, w = lit.shape
	x0, y0, x1, y1 = box
	# The middle of the box, away from hair and background at its edges
	mx, my = (x1 - x0) // 5, (y1 - y0) // 5
	fx0, fy0, fx1, fy1 = max(x0 + mx, 0), max(y0 + my, 0), min(x1 - mx, w), min(y1 - my, h)
	if fx1 - fx0 < 8 or fy1 - fy0 < 8:
		return None
	mask = np.ones((h, w), bool)
	# Everything outside the box grown by a fifth, so the face's own glow stays out of it
	mask[max(y0 - 2 * my, 0):min(y1 + 2 * my, h), max(x0 - 2 * mx, 0):min(x1 + 2 * mx, w)] = False
	if mask.sum() < 0.05 * h * w:
		return None
	lit_f = lit[fy0:fy1, fx0:fx1].astype(np.float64).mean()
	dark_f = dark[fy0:fy1, fx0:fx1].astype(np.float64).mean()
	lit_b = lit[mask].astype(np.float64).mean()
	dark_b = dark[mask].astype(np.float64).mean()
	if lit_f < 1 or lit_b < 1:
		return None
	return dark_f / lit_f, dark_b / lit_b


def pct(v, q):
	s = sorted(v)
	return s[min(len(s) - 1, int(q * len(s)))]


results = {}
skipped = {}
for sdir in sorted(glob.glob(os.path.join(args.faces, "*", "*"))):
	label = sdir.split(os.sep)[-2]
	try:
		with open(os.path.join(sdir, "meta.json")) as f:
			meta = json.load(f)
	except OSError:
		continue
	dark_seq = meta.get("dark_seq")
	if not dark_seq:
		continue
	lits = sorted(glob.glob(os.path.join(sdir, "*.pgm")))
	for i, path in enumerate(lits):
		if i >= len(dark_seq):
			break
		if dark_seq[i] < 0:
			continue
		lit, dark = read(path), read(os.path.join(sdir, "dark", "%04d.pgm" % dark_seq[i]))
		if lit is None or dark is None or lit.shape != dark.shape:
			continue
		r = ratios(lit, dark)
		if r is None:
			skipped[label] = skipped.get(label, 0) + 1
		else:
			results.setdefault(label, []).append(r)

if not results:
	sys.exit("liveness-eval: no lit/dark pairs found, record sessions with face-capture -keep-dark")

print("%-18s %5s  %-28s  %s" % ("label", "pairs", "face dark/lit  p10 med p90", "background dark/lit  med"))
for label in sorted(results):
	face = [r[0] for r in results[label]]
	back = [r[1] for r in results[label]]
	note = "" if len(face) >= args.min_pairs else "  (too few pairs to trust)"
	if skipped.get(label):
		note += "  (%d pairs without a face)" % skipped[label]
	print("%-18s %5d  %.2f  %.2f  %.2f                %.2f%s" % (
		label, len(face), pct(face, 0.1), statistics.median(face), pct(face, 0.9), statistics.median(back), note))

print("\nA face that's really lit by the emitter shows a low face ratio. An attack that glows by itself")
print("(a screen) stays near 1 or above it. Overlap between your label and an attack- label means")
print("this signal alone can't tell them apart on your camera.")
