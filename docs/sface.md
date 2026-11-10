# The sface recogniser

`recognizer = sface` swaps dlib's 2017 HOG detector and ResNet encoder for
two OpenCV Zoo models, run with ONNX Runtime:

- **YuNet** (`face_detection_yunet_2026may.onnx`, MIT) finds faces and five
  landmarks: the eyes, the nose tip, and the mouth corners.
- **SFace** (`face_recognition_sface_2021dec.onnx`, Apache-2.0) takes each
  face, aligned onto the ArcFace 112x112 template, and turns it into 128
  numbers of length 1.

dlib stays the default. Nothing changes unless `config.ini` asks for sface.

## Why

We ran `tools/face-eval.py` on one laptop's IR camera (640x360, strobing
emitter). One user enrolled with glasses and tested without, and the other way
round. There were also two other people, and a phone photo of the user held up:

| | dlib, certainty 3.5 | sface, match_threshold 0.363 |
|---|---|---|
| separation from the other people (d') | 5.3 | 9.1 |
| the user's frames, other glasses state | median certainty 3.98, none accepted | median similarity 0.58 to 0.65, 95% over 0.42 |
| closest other person | | similarity 0.24 |
| phone photo | no face found | no face found at YuNet score 0.85 |

dlib does tell the people apart. It fails because the user's own frames land
over the threshold once the glasses change. sface leaves a wide gap on both
sides of 0.363. One user and two other people is not a real benchmark, so run
the harness on your own camera before you rely on it.

## Comparing on your own camera

Record a few sessions of yourself (on different days, with glasses on and
off). Record other people with their consent, and optionally a photo or
screen of you held up to the camera. Frames stay in `~/.cache/howdy-go/faces`,
mode 0700. The report has scores, never encodings.

    go build -o face-capture ./cmd/face-capture
    ./face-capture -device /dev/videoN $USER        # once per session
    ./face-capture -device /dev/videoN guest1
    ./face-capture -device /dev/videoN attack-phone
    tools/fetch-face-models                          # -r adds ArcFace, non-commercial
    python3 -I tools/face-eval.py --upsample 0 --certainty 3.5 --dark-threshold 100

Pass the `[video]` values you actually unlock with. Otherwise the harness uses
whatever `config.ini` said when you recorded.

## Turning it on

The Arch and AUR packages depend on ONNX Runtime. They install both models in
`/usr/share/howdy-go/onnx`, pinned by sha256 to OpenCV Zoo commit 48b10fa,
with their licences next to howdy-go's. If you build without the package, put
them there yourself:

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

Then enroll again. Models from before have no sface descriptor, so they're
ignored. If you wear glasses only some of the time, run `sudo howdy add` once
with them and once without.

howdy-go needs ONNX Runtime (`onnxruntime-cpu` on Arch) at build and run
time. `-tags noonnx` builds without it, and then sface can't load.

## Matching

`match_threshold` is the lowest cosine similarity that counts as a match. It
runs from 0 to 1, and higher is stricter. 0.363 is the threshold the SFace
authors suggest. The descriptors have length 1, so compare checks the
Euclidean distance against `sqrt(2 - 2 * match_threshold)`. That's the same
test, and the frame loop is otherwise unchanged. `certainty` is ignored with
sface.

The end report, `howdy test`, and the snapshot text show similarity (higher is
better) instead of certainty (lower is better).

## Storage

The descriptors go in `/etc/howdy/models-sface/<user>.dat`. It's the same JSON
as `/etc/howdy/models`, and each entry is tagged `"encoder": "sface-2021dec"`.
It's a separate directory because howdy-gtk lists every file in
`/etc/howdy/models` as a user, and Python Howdy can't read entries that aren't
dlib's.

`/etc/howdy/models/<user>.dat` still decides what's enrolled:

- `howdy add` always writes a dlib model there, even with sface on. That keeps
  the PAM module working (it checks that the file exists), along with
  `howdy list`, howdy-gtk, and a switch back to dlib. It writes the `.dat`
  file first, then the sface descriptor with the same id, time, and label. One
  camera run feeds both detectors, and if either one sees two faces, it stops.
- `howdy remove` and `howdy clear` remove from both files.
- compare only uses sface descriptors whose id *and* time are still in the
  `.dat` file, and whose encoder tag matches. The id alone isn't enough. howdy
  reuses the newest model's id once that model is removed. So a descriptor left
  behind (by howdy-gtk or Python Howdy removing the model, or by a remove that
  failed halfway) could come back under a later model. `add` and `remove` also
  delete such leftovers from the sface file.
- With sface configured and no usable descriptor, compare exits 10 (no face
  model), so PAM asks for the password. It never falls back to dlib.

## Loading

Both files are opened with `O_NOFOLLOW`. They must be regular files under
64 MB, and they must match the sha256 compiled into howdy-compare before ONNX
Runtime sees them. ONNX Runtime runs with one intra-op thread and one inter-op
thread, sequential execution, and no spinning. Its log goes nowhere, because
PAM reads stdout.

On the same laptop, loading both models takes about 55 ms. That fits inside
the ~145 ms the camera takes to open. A 640x360 frame takes 4 to 10 ms to
detect and 17 to 43 ms to encode, depending on the CPU's power state
([performance.md](performance.md)). Peak address space grows by about 470 MiB,
which is well inside howdy-compare's 4 GiB RLIMIT_AS budget. The extra threads
are Go runtime threads, and `harden.Drop` checks every thread after dropping
root.

## How close it is to OpenCV

The Go code ports `cv::FaceDetectorYN` and `cv::FaceRecognizerSF`. It doesn't
link OpenCV. `TestMatchesOpenCV` checks it against python-opencv on the NASA
test portraits. Boxes and landmarks match within 0.0001 px, scores within
1e-7, and aligned crops are identical. (`imgproc.WarpAffine` ports OpenCV
4.11's float32 warp kernel bit for bit.) Feature cosine is 1.000000.

    tools/fetch-face-models
    python3 -I tools/sface-reference.py ~/.cache/howdy-go/testdata
    HOWDY_GO_TESTDATA=~/.cache/howdy-go/testdata HOWDY_GO_MODELS=~/.cache/howdy-go/models \
        go test -run 'MatchesOpenCV|RunSFace|Pinned' ./internal/sface ./internal/onnx

On the recordings above, the Go pipeline's scores agreed with face-eval.py's
to 3 decimal places.

Deliberate differences from howdy's dlib path: YuNet runs on the CLAHE-equalised
frame, the same one dlib's detector sees. SFace encodes from the raw frame, the
same as dlib's encoder does. The grey frame is copied into all three channels,
so OpenCV's BGR order doesn't matter.

## Not done

- **Liveness.** The phone photo is rejected only because YuNet scores it 0.62
  to 0.66, under the 0.85 cutoff. That isn't a liveness check, and a better
  print or screen could pass. A strobing IR emitter gives us one for free. A
  real face is lit in one frame and dark in the next, but a screen glows the
  same in both. There's no threshold yet, because it has to come from
  recordings of real attacks on your camera. The measuring half already exists:

      ./face-capture -keep-dark -device /dev/videoN $USER
      ./face-capture -keep-dark -device /dev/videoN attack-phone
      python3 -I tools/liveness-eval.py

  `-keep-dark` saves the off-phase frame before each lit one under `dark/`.
  `liveness-eval.py` prints, for each label, how much the brightness of the
  face region drops between the two. If your frames and the attacks' frames
  overlap, the signal can't tell them apart on that camera, and nothing should
  be built on it. If they don't overlap, the next step is a threshold taken
  from many more sessions than one laptop's worth, plus a `howdy-compare` check
  that fails closed on a camera whose emitter doesn't strobe. The dark frames
  show a face like any other, so they stay in `~/.cache/howdy-go`.
- **Other models.** The model files, hashes, input size, and encoder tag are
  constants in `internal/backend` and `internal/sface`. EdgeFace-base scored
  close to SFace and could replace it, with a new tag so old descriptors are
  never compared with new ones.
