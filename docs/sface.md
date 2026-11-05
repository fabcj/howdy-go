# The sface recogniser

`recognizer = sface` swaps dlib's 2017 HOG detector and ResNet encoder for
two OpenCV Zoo models run with ONNX Runtime:

- **YuNet** (`face_detection_yunet_2026may.onnx`, MIT) finds faces and five
  landmarks: the eyes, the nose tip and the mouth corners.
- **SFace** (`face_recognition_sface_2021dec.onnx`, Apache-2.0) turns each
  face, aligned onto the ArcFace 112x112 template, into 128 numbers of
  length 1.

dlib stays the default. Nothing changes unless `config.ini` asks for sface.

## Why

`tools/face-eval.py` on one laptop's IR camera (640x360, strobing emitter):
one user enrolled with glasses and tested without (and the other way
round), two other people, and a phone photo of the user held up:

| | dlib, certainty 3.5 | sface, match_threshold 0.363 |
|---|---|---|
| separation from the other people (d') | 5.3 | 9.1 |
| the user's frames, other glasses state | median certainty 3.98, none accepted | median similarity 0.58 to 0.65, 95% over 0.42 |
| closest other person | | similarity 0.24 |
| phone photo | no face found | no face found at YuNet score 0.85 |

dlib does tell the people apart; it fails because the user's own frames
land over the threshold once the glasses change. sface leaves a wide gap
on both sides of 0.363. One user and two other people is not a benchmark:
run the harness on your own camera before relying on it (README, "Face models").

## Turning it on

The Arch and AUR packages depend on ONNX Runtime and install both models
in `/usr/share/howdy-go/onnx`, pinned by sha256 to OpenCV Zoo commit
48b10fa, with their licenses next to howdy-go's. Built without the
package, put them there yourself:

    tools/fetch-face-models                 # checks the sha256 of each file
    sudo install -d -m 0755 /usr/share/howdy-go/onnx
    sudo install -m 0644 ~/.cache/howdy-go/models/yunet_2026may.onnx \
        /usr/share/howdy-go/onnx/face_detection_yunet_2026may.onnx
    sudo install -m 0644 ~/.cache/howdy-go/models/sface_2021dec.onnx \
        /usr/share/howdy-go/onnx/face_recognition_sface_2021dec.onnx

Then, with `sudo howdy config`:

    [core]
    recognizer = sface

    [video]
    match_threshold = 0.363

and enroll again: models from before have no sface descriptor and are
ignored. If you wear glasses only some of the time, run `sudo howdy add`
once with them and once without.

howdy-go needs ONNX Runtime (`onnxruntime-cpu` on Arch) at build and run
time. `-tags noonnx` builds without it, and then sface can't load.

## Matching

`match_threshold` is the least cosine similarity that counts as a match,
between 0 and 1, higher is stricter. 0.363 is the threshold the SFace
authors give. Since the descriptors have length 1, compare checks the
Euclidean distance against `sqrt(2 - 2 * match_threshold)`, which is the
same test, and the frame loop is otherwise unchanged. `certainty` is
ignored with sface.

The end report, `howdy test` and the snapshot text show similarity (higher
is better) instead of certainty (lower is better).

## Storage

The descriptors go in `/etc/howdy/models-sface/<user>.dat`, the same JSON
as `/etc/howdy/models`, each entry tagged `"encoder": "sface-2021dec"`. A
separate directory because howdy-gtk lists every file in
`/etc/howdy/models` as a user, and Python Howdy can't read entries that
aren't dlib's.

`/etc/howdy/models/<user>.dat` stays the one that decides what's
enrolled:

- `howdy add` always writes a dlib model there, also under sface, so the
  PAM module (which checks that file exists), `howdy list`, howdy-gtk and
  a switch back to dlib all keep working. It writes the `.dat` file first,
  then the sface descriptor with the same id, time and label. One camera
  run feeds both detectors, and either one seeing two faces stops it.
- `howdy remove` and `howdy clear` remove from both files.
- compare only uses sface descriptors whose id *and* time are still in the
  `.dat` file and whose encoder tag matches. The id alone isn't enough:
  howdy reuses the newest model's id once it's removed, so a descriptor
  left behind (by howdy-gtk or Python Howdy removing the model, or a
  remove that failed halfway) would otherwise come back under a later
  model. `add` and `remove` also delete such leftovers from the sface
  file.
- With sface configured and no usable descriptor, compare exits 10 (no face
  model), so PAM asks for the password. It never falls back to dlib.

## Loading

Both files are opened with `O_NOFOLLOW`, must be regular files under
64 MB, and must match the sha256 compiled into howdy-compare before ONNX
Runtime sees them. ONNX Runtime runs with one intra-op and one inter-op
thread, sequential execution and no spinning, and its log goes nowhere,
since PAM reads stdout.

Measured on the same laptop: loading both models takes about 55 ms, inside
the ~145 ms the camera takes to open, and a 640x360 frame takes 5 ms to
detect and about 13 ms to encode. Peak address space grows by about 470 MiB,
well inside howdy-compare's 4 GiB RLIMIT_AS budget. The threads it adds are
Go runtime threads, and `harden.Drop` checks every thread after dropping
root.

## How close it is to OpenCV

The Go code ports `cv::FaceDetectorYN` and `cv::FaceRecognizerSF` rather
than linking OpenCV. `TestMatchesOpenCV` checks it against python-opencv on
the NASA test portraits: boxes and landmarks within 0.0001 px, scores within
1e-7, aligned crops identical (`imgproc.WarpAffine` ports OpenCV 4.11's
float32 warp kernel bit for bit), feature cosine 1.000000.

    tools/fetch-face-models
    python3 -I tools/sface-reference.py ~/.cache/howdy-go/testdata
    HOWDY_GO_TESTDATA=~/.cache/howdy-go/testdata HOWDY_GO_MODELS=~/.cache/howdy-go/models \
        go test -run 'MatchesOpenCV|RunSFace|Pinned' ./internal/sface ./internal/onnx

On the recordings above, the Go pipeline's scores agreed with
face-eval.py's to 3 decimals.

Deliberate differences from howdy's dlib path: YuNet runs on the
CLAHE-equalised frame, the same one dlib's detector sees, and SFace encodes
from the raw frame, as dlib's encoder does. The grey frame is copied into
all three channels, so OpenCV's BGR order doesn't matter.

## Not done

- **Liveness.** The phone photo is rejected only because YuNet scores it
  0.62 to 0.66, under the 0.85 cutoff. That isn't a liveness check, and a
  better print or screen could pass. A strobing IR emitter gives one for
  free: a real face is lit in one frame and dark in the next, while a
  screen glows the same in both. Calibrating that needs recordings that keep
  the dark frames next to the lit ones, of you and of attacks, so it's a
  follow-up rather than a guessed threshold.
- **Other models.** The model files, hashes, input size and encoder tag are
  constants in `internal/backend` and `internal/sface`. EdgeFace-base
  scored close to SFace and could replace it, with a new tag so old
  descriptors are never compared with new ones.
