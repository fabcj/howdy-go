# Compares face pipelines on frames recorded with face-capture: dlib as
# compare runs it, YuNet feeding dlib, and YuNet with the ONNX recognisers
# tools/fetch-face-models downloads. Prints how well each one separates the
# user from everyone else, and writes the numbers (scores only, never
# encodings or frames) to a JSON file.
#
#   python3 -I tools/face-eval.py [--user LABEL] [--faces DIR] [--models DIR]
#                                 [--impostor-images DIR] [--only a,b] [--out FILE]
#
# The faces dir holds <label>/<session>/*.pgm. The user's earliest session
# (or --enroll) is enrolled the way howdy add does it; the user's other
# sessions are the genuine attempts, other labels are impostors and labels
# starting with attack- are photos or screens held up to the camera.
#
# Like the real thing, enrollment sees frames at the camera's own size (howdy
# add ignores max_height) and the attempts see them shrunk to max_height.
# Frames darker than --dark-threshold are skipped, as compare and howdy add
# skip them. Pass --dark-threshold, --upsample and --certainty when the
# config.ini face-capture read isn't the one you unlock with.
import argparse
import glob
import json
import math
import os
import statistics
import sys
import time

# Like compare: one BLAS thread, set before dlib loads
for var in ("OPENBLAS_NUM_THREADS", "OMP_NUM_THREADS", "GOTO_NUM_THREADS"):
	os.environ.setdefault(var, "1")

import cv2
import dlib
import numpy as np

cv2.setNumThreads(1)

cache = os.environ.get("XDG_CACHE_HOME") or os.path.join(os.path.expanduser("~"), ".cache")
p = argparse.ArgumentParser()
p.add_argument("--user", default=os.environ.get("USER", ""))
p.add_argument("--faces", default=os.path.join(cache, "howdy-go", "faces"))
p.add_argument("--models", default=os.path.join(cache, "howdy-go", "models"))
p.add_argument("--dlib-data", default="/usr/share/dlib-data")
p.add_argument("--enroll", help="session name to enroll from, default the user's earliest")
p.add_argument("--enroll-frames", type=int, default=15)
p.add_argument("--required-matches", type=int, default=1)
p.add_argument("--certainty", type=float, help="default: from the enrollment session's meta.json")
p.add_argument("--upsample", type=int, help="default: from the enrollment session's meta.json")
p.add_argument("--dark-threshold", type=float, help="default: from the enrollment session's meta.json")
p.add_argument("--max-height", type=float, help="default: from each session's meta.json")
p.add_argument("--yunet-score", type=float, default=0.6)
p.add_argument("--impostor-images", help="dir of .jpg portraits to add as impostors (visible light, so weak ones)")
p.add_argument("--only", help="comma separated pipeline names")
p.add_argument("--cnn", action="store_true", help="also run dlib's MMOD detector (slow)")
p.add_argument("--out", default=os.path.join(cache, "howdy-go", "face-eval.json"))
args = p.parse_args()

# ArcFace's 5 point template for a 112x112 crop: eye on the image's left
# first, then the other eye, nose tip, mouth corners in the same order.
# YuNet gives its points in this order too.
ARCFACE_112 = np.array([
	[38.2946, 51.6963], [73.5318, 51.5014], [56.0252, 71.7366],
	[41.5493, 92.3655], [70.7299, 92.2041],
], dtype=np.float64)


# Frames

class Frame:
	def __init__(self, label, session, name, raw, max_height):
		self.label, self.session, self.name = label, session, name
		gs = cv2.createCLAHE(clipLimit=2.0, tileGridSize=(8, 8)).apply(raw)
		# compare's darkness: percent of pixels under 32 after CLAHE, float32
		self.darkness = np.float32(np.count_nonzero(gs < 32)) / np.float32(gs.size) * np.float32(100)
		# compare.go only ever shrinks
		if max_height and max_height < raw.shape[0]:
			f = max_height / raw.shape[0]
			size = (round(raw.shape[1] * f), round(raw.shape[0] * f))
			raw = cv2.resize(raw, size, interpolation=cv2.INTER_AREA)
			gs = cv2.resize(gs, size, interpolation=cv2.INTER_AREA)
		self.raw, self.gs = raw, gs
		self.raw3 = cv2.cvtColor(raw, cv2.COLOR_GRAY2BGR)
		self.gs3 = cv2.cvtColor(gs, cv2.COLOR_GRAY2BGR)


def load_sessions(root):
	sessions = []
	for sdir in sorted(glob.glob(os.path.join(root, "*", "*"))):
		label, session = sdir.split(os.sep)[-2:]
		meta = {}
		try:
			with open(os.path.join(sdir, "meta.json")) as f:
				meta = json.load(f)
		except OSError:
			pass
		raws = []
		for path in sorted(glob.glob(os.path.join(sdir, "*.pgm"))):
			raw = cv2.imread(path, cv2.IMREAD_GRAYSCALE)
			if raw is not None:
				raws.append((os.path.basename(path), raw))
		if not raws:
			continue
		times = meta.get("times_ms")
		if not times or len(times) != len(raws):
			times = None
		mh = args.max_height if args.max_height is not None else meta.get("max_height", 320)
		sessions.append({
			"label": label, "session": session, "meta": meta, "raws": raws, "times": times,
			"frames": [Frame(label, session, n, r, mh) for n, r in raws],
		})
	return sessions


def load_portraits(d):
	frames = []
	for path in sorted(glob.glob(os.path.join(d, "*.jpg"))):
		grey = cv2.imread(path, cv2.IMREAD_GRAYSCALE)
		if grey is not None:
			frames.append(Frame("portraits", os.path.basename(path), os.path.basename(path), grey, 480))
	return frames


# Detectors. Each returns a list of faces as dicts with "box" (left, top,
# right, bottom, edges included as dlib has them) and maybe "kps" and "row".

class Timer:
	def __init__(self):
		self.ms = []

	def __call__(self, fn, *a):
		t = time.perf_counter()
		r = fn(*a)
		self.ms.append((time.perf_counter() - t) * 1000)
		return r

	def median(self):
		return statistics.median(self.ms) if self.ms else float("nan")


class HOG:
	def __init__(self, upsample):
		self.det = dlib.get_frontal_face_detector()
		self.upsample = upsample

	def __call__(self, fr):
		return [{"box": (r.left(), r.top(), r.right(), r.bottom())} for r in self.det(fr.gs, self.upsample)]


class CNN:
	def __init__(self, path, upsample):
		self.det = dlib.cnn_face_detection_model_v1(path)
		self.upsample = upsample

	def __call__(self, fr):
		return [{"box": (d.rect.left(), d.rect.top(), d.rect.right(), d.rect.bottom())} for d in self.det(fr.gs, self.upsample)]


class YuNet:
	def __init__(self, path, score):
		self.det = cv2.FaceDetectorYN.create(path, "", (320, 320), score, 0.3, 5000)

	def __call__(self, fr):
		h, w = fr.gs.shape
		self.det.setInputSize((w, h))
		_, rows = self.det.detect(fr.gs3)
		out = []
		for row in [] if rows is None else rows:
			x, y, bw, bh = row[:4]
			out.append({
				"box": (int(round(x)), int(round(y)), int(round(x + bw)) - 1, int(round(y + bh)) - 1),
				"kps": row[4:14].reshape(5, 2).astype(np.float64),
				"row": row,
			})
		return out


# Recognisers. Each turns (frame, face) into a vector; "euclid" ones are
# scored by Euclidean distance like dlib, "cosine" ones are L2 normalised.

class DlibEncoder:
	metric = "euclid"

	def __init__(self, data, box_fit=None):
		self.sp = dlib.shape_predictor(os.path.join(data, "shape_predictor_5_face_landmarks.dat"))
		self.enc = dlib.face_recognition_model_v1(os.path.join(data, "dlib_face_recognition_resnet_model_v1.dat"))
		self.box_fit = box_fit

	def __call__(self, fr, face):
		l, t, r, b = face["box"]
		if self.box_fit is not None:
			w, h = r - l + 1, b - t + 1
			fl, ft, fr_, fb = self.box_fit
			l, t, r, b = (round(l + fl * w), round(t + ft * h), round(l + fr_ * w), round(t + fb * h))
		shape = self.sp(fr.raw3, dlib.rectangle(l, t, r, b))
		return np.array(self.enc.compute_face_descriptor(fr.raw3, shape, 1), dtype=np.float64)


def umeyama(src, dst):
	"""Least squares similarity transform taking src to dst, as skimage's
	SimilarityTransform (what InsightFace and UniFace align with)."""
	n = src.shape[0]
	ms, md = src.mean(0), dst.mean(0)
	sc, dc = src - ms, dst - md
	cov = dc.T @ sc / n
	u, s, vt = np.linalg.svd(cov)
	d = np.ones(2)
	if np.linalg.det(cov) < 0:
		d[1] = -1
	rot = u @ np.diag(d) @ vt
	scale = (s * d).sum() / sc.var(0).sum()
	m = np.zeros((2, 3))
	m[:, :2] = scale * rot
	m[:, 2] = md - scale * rot @ ms
	return m


class OnnxEncoder:
	metric = "cosine"

	def __init__(self, path, clahe):
		self.net = cv2.dnn.readNetFromONNX(path)
		self.clahe = clahe

	def __call__(self, fr, face):
		img = fr.gs3 if self.clahe else fr.raw3
		m = umeyama(face["kps"], ARCFACE_112)
		crop = cv2.warpAffine(img, m, (112, 112), flags=cv2.INTER_LINEAR, borderValue=0)
		blob = cv2.dnn.blobFromImage(crop, 1 / 127.5, (112, 112), (127.5, 127.5, 127.5), swapRB=False)
		self.net.setInput(blob)
		v = self.net.forward().reshape(-1).astype(np.float64)
		return v / np.linalg.norm(v)


class SFace:
	metric = "cosine"

	def __init__(self, path, clahe):
		self.rec = cv2.FaceRecognizerSF.create(path, "")
		self.clahe = clahe

	def __call__(self, fr, face):
		img = fr.gs3 if self.clahe else fr.raw3
		crop = self.rec.alignCrop(img, face["row"])
		v = self.rec.feature(crop).reshape(-1).astype(np.float64)
		return v / np.linalg.norm(v)


# Enrollment, as internal/enroll: drop frames far from the mean, average the
# rest. For cosine models the vectors are normalised before and after.

def enroll(vectors, metric):
	vs = np.array(vectors)
	dist = lambda a, b: np.linalg.norm(b - a, axis=1)
	mean = vs.mean(0)
	if metric == "cosine":
		mean /= np.linalg.norm(mean)
	d = dist(mean, vs)
	limit = max(statistics.median(d) * 1.5, sorted(d)[(len(d) - 1) // 2])
	kept = vs[d <= limit]
	avg = kept.mean(0)
	if metric == "cosine":
		avg /= np.linalg.norm(avg)
	return avg, kept


def score(metric, template, v):
	"""Higher is more alike: minus the distance for dlib, cosine otherwise"""
	if metric == "euclid":
		return -float(np.linalg.norm(v - template))
	return float(v @ template)


# Metrics

def far_frr(gen, imp, thr):
	far = sum(s >= thr for s in imp) / len(imp) if imp else float("nan")
	frr = sum(s < thr for s in gen) / len(gen) if gen else float("nan")
	return far, frr


def threshold_far0(imp):
	"""The lowest threshold no impostor frame reaches"""
	return np.nextafter(max(imp), math.inf) if imp else None


def eer(gen, imp):
	if not gen or not imp:
		return None
	best = (2, None)
	for t in sorted(set(gen) | set(imp)):
		far, frr = far_frr(gen, imp, t)
		if abs(far - frr) < best[0]:
			best = (abs(far - frr), (far + frr) / 2)
	return best[1]


def dprime(gen, imp):
	g = [s for s in gen if s > -math.inf]
	i = [s for s in imp if s > -math.inf]
	if len(g) < 2 or len(i) < 2:
		return None
	return (statistics.mean(g) - statistics.mean(i)) / math.sqrt((statistics.variance(g) + statistics.variance(i)) / 2)


def unlock_times(sessions, thr, need):
	"""For every start frame in every genuine session, how many lit frames
	and how many ms (None without timestamps) until need of them pass thr.
	(None, None) when the session runs out first."""
	out = []
	for scores, times in sessions:
		for k in range(len(scores)):
			hits = 0
			got = (None, None)
			for j in range(k, len(scores)):
				if scores[j] >= thr:
					hits += 1
					if hits >= need:
						got = (j - k + 1, times[j] - times[k] if times else None)
						break
			out.append(got)
	return out


def unlock_summary(sessions, thr, need):
	u = unlock_times(sessions, thr, need)
	if not u:
		return {}
	frames = [f for f, _ in u if f]
	ms = [t for f, t in u if f and t is not None]
	return {
		"unlock_median_frames": statistics.median(frames) if frames else None,
		"unlock_within_3": sum(1 for f in frames if f <= 3) / len(u),
		"unlock_median_ms": statistics.median(ms) if ms else None,
		"unlock_p90_ms": sorted(ms)[int(len(ms) * 0.9)] if ms else None,
		"unlock_never": sum(1 for f, _ in u if not f) / len(u),
	}


# Main

def main():
	sessions = load_sessions(args.faces)
	mine = [s for s in sessions if s["label"] == args.user]
	if not mine:
		sys.exit("face-eval: no sessions for %r in %s (record some with face-capture)" % (args.user, args.faces))

	meta = (next((s for s in mine if s["session"] == args.enroll), mine[0]) if args.enroll else mine[0])["meta"]
	certainty = args.certainty if args.certainty is not None else meta.get("certainty", 3.5)
	upsample = args.upsample if args.upsample is not None else meta.get("detection_upsample", 1)
	dark_threshold = args.dark_threshold if args.dark_threshold is not None else meta.get("dark_threshold", 60)
	from_meta = [k for k, v in (("certainty", args.certainty), ("upsample", args.upsample),
		("dark threshold", args.dark_threshold)) if v is None]

	# What compare and howdy add would skip as too dark
	skipped = 0
	for sess in sessions:
		keep = [i for i, fr in enumerate(sess["frames"]) if fr.darkness < 100 and fr.darkness <= dark_threshold]
		skipped += len(sess["frames"]) - len(keep)
		sess["frames"] = [sess["frames"][i] for i in keep]
		sess["raws"] = [sess["raws"][i] for i in keep]
		if sess["times"]:
			sess["times"] = [sess["times"][i] for i in keep]
	for sess in sessions:
		if len(sess["frames"]) < 3:
			print("skipping %s/%s: %d usable frames" % (sess["label"], sess["session"], len(sess["frames"])))
	sessions = [sess for sess in sessions if len(sess["frames"]) >= 3]

	mine = [s for s in sessions if s["label"] == args.user]
	if not mine:
		sys.exit("face-eval: no session of %r has 3 frames at dark threshold %g" % (args.user, dark_threshold))
	enroll_session = next((s for s in mine if s["session"] == args.enroll), mine[0]) if args.enroll else mine[0]
	genuine = [s for s in mine if s is not enroll_session]
	impostors = [s for s in sessions if s["label"] != args.user and not s["label"].startswith("attack-")]
	attacks = [s for s in sessions if s["label"].startswith("attack-")]
	extra = load_portraits(args.impostor_images) if args.impostor_images else []

	# howdy add works on the camera's own size
	es = enroll_session
	enroll_frames = [Frame(es["label"], es["session"], n, r, None) for n, r in es["raws"]]
	same_session = False
	if not genuine:
		# One session: enroll on its start, test on the rest. Optimistic.
		same_session = True
		cut = min(len(enroll_frames) // 2, args.enroll_frames * 2)
		genuine = [{"label": args.user, "session": es["session"] + " (rest)", "frames": es["frames"][cut:],
			"times": es["times"][cut:] if es["times"] else None}]
		enroll_frames = enroll_frames[:cut]

	imp_frames = [f for s in impostors for f in s["frames"]] + extra
	att_frames = [f for s in attacks for f in s["frames"]]
	n_people = len({s["label"] for s in impostors}) + (1 if extra else 0)

	if len(enroll_frames) < 3:
		sys.exit("face-eval: %s/%s has %d frames at dark threshold %g, record a longer session or raise --dark-threshold" % (
			args.user, es["session"], len(enroll_frames), dark_threshold))
	print("enroll: %s/%s, %d frames, up to %d used" % (args.user, enroll_session["session"], len(enroll_frames), args.enroll_frames))
	print("genuine: %d frames in %d sessions%s" % (sum(len(s["frames"]) for s in genuine), len(genuine),
		" (same session as enrollment, optimistic)" if same_session else ""))
	print("impostors: %d frames from %d people, attacks: %d frames" % (len(imp_frames), n_people, len(att_frames)))
	if not imp_frames:
		print("no impostors: false accepts can't be measured, record someone else or pass --impostor-images")
	print("dlib: certainty %g, upsample %d, required_matches %d, dark threshold %g (%d frames skipped)%s\n" % (
		certainty, upsample, args.required_matches, dark_threshold, skipped,
		", %s from the capture's config.ini" % ", ".join(from_meta) if from_meta else ""))

	# Detectors, each run once per frame and shared by the pipelines on it
	m = args.models
	yunet_path = next((os.path.join(m, y) for y in ("yunet_2026may.onnx", "yunet_2023mar.onnx") if os.path.exists(os.path.join(m, y))), None)
	detectors = {"hog": HOG(upsample)}
	if args.cnn:
		detectors["cnn"] = CNN(os.path.join(args.dlib_data, "mmod_human_face_detector.dat"), upsample)
	if yunet_path:
		detectors["yunet"] = YuNet(yunet_path, args.yunet_score)
	det_time = {k: Timer() for k in detectors}
	det_cache = {}

	def detect(kind, fr):
		key = (kind, id(fr))
		if key not in det_cache:
			det_cache[key] = det_time[kind](detectors[kind], fr)
		return det_cache[key]

	# The HOG box in YuNet box units, fitted on the enrollment frames, so
	# dlib's shape predictor sees the box it was trained on
	box_fit = None
	if "yunet" in detectors:
		rel = []
		for fr in enroll_frames:
			h, y = detect("hog", fr), detect("yunet", fr)
			if len(h) == 1 and len(y) == 1:
				hl, ht, hr, hb = h[0]["box"]
				yl, yt, yr, yb = y[0]["box"]
				w, hh = yr - yl + 1, yb - yt + 1
				rel.append(((hl - yl) / w, (ht - yt) / hh, (hr - yl) / w, (hb - yt) / hh))
		if rel:
			box_fit = tuple(statistics.median(c) for c in zip(*rel))
			print("HOG box from YuNet box (fraction of its width/height): left %.3f top %.3f right %.3f bottom %.3f, from %d frames\n" % (box_fit + (len(rel),)))

	pipelines = [("dlib-hog", "hog", lambda: DlibEncoder(args.dlib_data))]
	if args.cnn:
		pipelines.append(("dlib-cnn", "cnn", lambda: DlibEncoder(args.dlib_data)))
	if "yunet" in detectors:
		pipelines.append(("yunet+dlib", "yunet", lambda: DlibEncoder(args.dlib_data, box_fit)))
		pipelines.append(("yunet+dlib-rawbox", "yunet", lambda: DlibEncoder(args.dlib_data)))
		for name in ("sface_2021dec", "edgeface_xxs", "edgeface_xs_gamma_06", "edgeface_s_gamma_05", "edgeface_base",
				"adaface_ir_18", "adaface_ir_50", "arcface_mbf", "arcface_r50"):
			path = os.path.join(m, name + ".onnx")
			if not os.path.exists(path):
				continue
			cls = SFace if name.startswith("sface") else OnnxEncoder
			for clahe in (False, True):
				pipelines.append(("yunet+%s%s" % (name, "+clahe" if clahe else ""), "yunet",
					(lambda c, pth, cl: lambda: c(pth, cl))(cls, path, clahe)))
	if args.only:
		want = set(args.only.split(","))
		pipelines = [pl for pl in pipelines if pl[0] in want]

	results = []
	for name, kind, make in pipelines:
		t = time.perf_counter()
		rec = make()
		load_ms = (time.perf_counter() - t) * 1000
		rec_time = Timer()

		vectors = []
		for fr in enroll_frames:
			faces = detect(kind, fr)
			if len(faces) == 1:
				vectors.append(rec_time(rec, fr, faces[0]))
				if len(vectors) == args.enroll_frames:
					break
		if not vectors:
			print("%-34s no single-face frames to enroll on" % name)
			results.append({"pipeline": name, "error": "nothing to enroll"})
			continue
		template, kept = enroll(vectors, rec.metric)

		def frame_score(fr):
			best_mean = best_all = -math.inf
			for face in detect(kind, fr):
				v = rec_time(rec, fr, face)
				best_mean = max(best_mean, score(rec.metric, template, v))
				best_all = max(best_all, max(score(rec.metric, k, v) for k in kept))
			return best_mean, best_all

		gen_by_session = [([frame_score(fr) for fr in s["frames"]], s.get("times")) for s in genuine]
		gen = [x for s, _ in gen_by_session for x in s]
		imp = [frame_score(fr) for fr in imp_frames]
		att = [frame_score(fr) for fr in att_frames]
		g_mean, g_all = [x[0] for x in gen], [x[1] for x in gen]
		i_mean, i_all = [x[0] for x in imp], [x[1] for x in imp]

		r = {
			"pipeline": name, "metric": rec.metric, "load_ms": round(load_ms, 1),
			"detect_ms": round(det_time[kind].median(), 2), "encode_ms": round(rec_time.median(), 2),
			"enrolled": len(vectors), "kept": len(kept),
			"genuine_frames": len(gen), "impostor_frames": len(imp), "attack_frames": len(att),
			"genuine_detected": sum(s > -math.inf for s in g_mean) / len(gen) if gen else None,
			# a detector that misses impostors makes the recogniser behind it look better
			"impostor_detected": sum(s > -math.inf for s in i_mean) / len(imp) if imp else None,
			"dprime": dprime(g_mean, i_mean), "eer": eer(g_mean, i_mean),
		}
		thr = threshold_far0(i_mean)
		thr_all = threshold_far0(i_all)
		if rec.metric == "euclid":
			cfg = -certainty / 10
			far, frr = far_frr(g_mean, i_mean, cfg)
			r["at_certainty"] = {"certainty": certainty, "far": far, "frr": frr,
				"attacks_accepted": sum(x[0] >= cfg for x in att) / len(att) if att else None}
			r["at_certainty"].update(unlock_summary([([x[0] for x in s], t) for s, t in gen_by_session], cfg, args.required_matches))
		if thr is not None:
			r["far0"] = {
				"threshold": float(-thr * 10 if rec.metric == "euclid" else thr),
				"frr": far_frr(g_mean, i_mean, thr)[1],
				"frr_all_templates": far_frr(g_all, i_all, thr_all)[1],
				"attacks_accepted": sum(x[0] >= thr for x in att) / len(att) if att else None,
			}
			r["far0"].update(unlock_summary([([x[0] for x in s], t) for s, t in gen_by_session], thr, args.required_matches))
		g_ok = sorted(s for s in g_mean if s > -math.inf)
		if g_ok:
			conv = (lambda s: -s * 10) if rec.metric == "euclid" else (lambda s: s)
			r["genuine_median"] = conv(statistics.median(g_ok))
			r["genuine_p5"] = conv(g_ok[len(g_ok) // 20])
		results.append(r)
		print_row(r)

	print("\nthreshold: certainty units (distance x 10, lower passes) for dlib, cosine similarity (higher passes) otherwise")
	print("FAR0 is the strictest threshold no impostor frame passes, on %d frames from %d people" % (len(imp_frames), n_people))
	os.makedirs(os.path.dirname(args.out), mode=0o700, exist_ok=True)
	fd = os.open(args.out, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
	with os.fdopen(fd, "w") as f:
		json.dump({"user": args.user, "certainty": certainty, "upsample": upsample, "dark_threshold": dark_threshold,
			"required_matches": args.required_matches, "same_session": same_session,
			"impostor_people": n_people, "box_fit": box_fit, "results": results}, f, indent=1)
	print("written to", args.out)


header_done = False


def print_row(r):
	global header_done
	pct = lambda v: "   -" if v is None or (isinstance(v, float) and math.isnan(v)) else "%3.0f%%" % (v * 100)
	num = lambda v, fmt: "-" if v is None else fmt % v
	if not header_done:
		print("%-34s %5s %5s %5s %5s | %7s %5s %6s %5s %5s %6s %5s | %5s %5s %5s" % (
			"pipeline", "det", "idet", "d'", "EER", "thr", "FRR", "FRRall", "unl", "<=3", "unl ms", "atk", "load", "det", "enc"))
		header_done = True
	f0 = r.get("far0", {})
	print("%-34s %5s %5s %5s %5s | %7s %5s %6s %5s %5s %6s %5s | %5s %5s %5s" % (
		r["pipeline"], pct(r["genuine_detected"]), pct(r["impostor_detected"]), num(r["dprime"], "%.2f"), pct(r["eer"]),
		num(f0.get("threshold"), "%.3f"), pct(f0.get("frr")), pct(f0.get("frr_all_templates")),
		num(f0.get("unlock_median_frames"), "%g"), pct(f0.get("unlock_within_3")), num(f0.get("unlock_median_ms"), "%d"),
		pct(f0.get("attacks_accepted")),
		num(r["load_ms"], "%.0f"), num(r["detect_ms"], "%.1f"), num(r["encode_ms"], "%.1f")))
	c = r.get("at_certainty")
	if c:
		print("%-34s at certainty %g: FAR %s FRR %s, unlock median %s frames / %s ms, %s within 3, attacks %s" % (
			"", c["certainty"], pct(c["far"]), pct(c["frr"]), num(c.get("unlock_median_frames"), "%g"),
			num(c.get("unlock_median_ms"), "%d"), pct(c.get("unlock_within_3")), pct(c["attacks_accepted"])))


main()
