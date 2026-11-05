# Runs the test images through python-dlib the way compare.py does and
# writes what the Go parity test checks against: each image as the grey
# frame both sides use (.pgm), and reference.json with boxes and encodings.
#
#   python3 -I tools/dlib-reference.py <testdata dir> [dlib data dir]
import glob
import json
import os
import sys

import cv2
import dlib
import numpy as np

HEIGHT = 480

d = sys.argv[1]
data = sys.argv[2] if len(sys.argv) > 2 else "/usr/share/dlib-data"

hog = dlib.get_frontal_face_detector()
cnn = dlib.cnn_face_detection_model_v1(os.path.join(data, "mmod_human_face_detector.dat"))
sp = dlib.shape_predictor(os.path.join(data, "shape_predictor_5_face_landmarks.dat"))
enc = dlib.face_recognition_model_v1(os.path.join(data, "dlib_face_recognition_resnet_model_v1.dat"))
clahe = cv2.createCLAHE(clipLimit=2.0, tileGridSize=(8, 8))

out = {}
for path in sorted(glob.glob(os.path.join(d, "*.jpg"))):
	name = os.path.splitext(os.path.basename(path))[0]
	grey = cv2.imread(path, cv2.IMREAD_GRAYSCALE)
	scale = HEIGHT / grey.shape[0]
	grey = cv2.resize(grey, (round(grey.shape[1] * scale), HEIGHT), interpolation=cv2.INTER_AREA)
	with open(os.path.join(d, name + ".pgm"), "wb") as f:
		f.write(b"P5\n%d %d\n255\n" % (grey.shape[1], grey.shape[0]))
		f.write(np.ascontiguousarray(grey).tobytes())

	gs = clahe.apply(grey)
	frame = cv2.cvtColor(grey, cv2.COLOR_GRAY2BGR)
	result = {}
	for kind, upsample in (("hog", 0), ("hog", 1), ("cnn", 0)):
		faces = []
		for r in (hog if kind == "hog" else cnn)(gs, upsample):
			if kind == "cnn":
				r = r.rect
			shape = sp(frame, r)
			faces.append({
				"box": [r.left(), r.top(), r.right(), r.bottom()],
				"parts": [[shape.part(i).x, shape.part(i).y] for i in range(5)],
				"encoding": list(enc.compute_face_descriptor(frame, shape, 1)),
			})
		result["%s%d" % (kind, upsample)] = faces
	out[name] = result

with open(os.path.join(d, "reference.json"), "w") as f:
	json.dump(out, f)
for name, r in out.items():
	print(name, {k: len(v) for k, v in r.items()})
