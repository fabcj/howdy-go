# Runs the test images through OpenCV's YuNet and SFace the way the sface
# backend uses them and writes what the Go test checks against:
# sface-reference.json with each image's detections, aligned crops and
# features. Run tools/dlib-reference.py first, it writes the .pgm frames.
#
#   python3 -I tools/sface-reference.py <testdata dir> [models dir]
import glob
import json
import os
import sys

import cv2
import numpy as np

cv2.setNumThreads(1)

d = sys.argv[1]
models = sys.argv[2] if len(sys.argv) > 2 else os.path.join(os.path.expanduser("~"), ".cache", "howdy-go", "models")
# Low, so the crew photo gives several faces to check NMS and the order on
SCORE = 0.5

det = cv2.FaceDetectorYN.create(os.path.join(models, "yunet_2026may.onnx"), "", (320, 320), SCORE, 0.3, 5000)
rec = cv2.FaceRecognizerSF.create(os.path.join(models, "sface_2021dec.onnx"), "")
clahe = cv2.createCLAHE(clipLimit=2.0, tileGridSize=(8, 8))

out = {"score_threshold": SCORE}
for path in sorted(glob.glob(os.path.join(d, "*.pgm"))):
	name = os.path.splitext(os.path.basename(path))[0]
	grey = cv2.imread(path, cv2.IMREAD_GRAYSCALE)
	gs = clahe.apply(grey)
	h, w = gs.shape
	det.setInputSize((w, h))
	_, rows = det.detect(cv2.cvtColor(gs, cv2.COLOR_GRAY2BGR))
	faces = []
	for row in [] if rows is None else rows:
		crop = rec.alignCrop(cv2.cvtColor(grey, cv2.COLOR_GRAY2BGR), row)
		faces.append({
			"row": [float(v) for v in row],
			"crop": cv2.cvtColor(crop, cv2.COLOR_BGR2GRAY).flatten().tolist(),
			"feature": [float(v) for v in rec.feature(crop).flatten()],
		})
	out[name] = faces

with open(os.path.join(d, "sface-reference.json"), "w") as f:
	json.dump(out, f)
for name, faces in out.items():
	if name != "score_threshold":
		print(name, len(faces), "faces")
