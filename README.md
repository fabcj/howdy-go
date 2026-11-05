# howdy-go

howdy-go provides Windows Hello™ style authentication for Linux. Use your
built-in IR emitters and camera in combination with facial recognition to
prove who you are.

Using the central authentication system (PAM), this works everywhere you
would otherwise need your password: login, lock screen, sudo, su, etc.

It is [Howdy](https://github.com/boltgolt/howdy) with its Python half
rewritten in Go: `howdy-compare`, which PAM runs on every unlock, and the
`howdy` command. It reads and writes the same files as Howdy
(`/etc/howdy/config.ini`, `/etc/howdy/models/<user>.dat`, the dlib model
files and snapshots), so your existing face models keep working, and the
PAM module and howdy-gtk are Howdy's own.

The point is speed. Python Howdy spends about 320 ms starting the
interpreter and importing numpy, OpenCV and dlib before it looks at a single
frame. howdy-go opens the camera while it loads the face models. On a laptop
with a 15 fps IR camera, `sudo true` with a face match takes 460 ms, against
790 ms for Python Howdy.

## Installation

### Arch Linux

Install the `howdy-go-git` AUR package. It replaces `howdy` or `howdy-git`,
and builds the PAM module and howdy-gtk from upstream Howdy at a pinned
commit.

    git clone https://aur.archlinux.org/howdy-go-git.git
    cd howdy-go-git && makepkg -si

Install `blas-openblas` too. Arch's dlib uses the reference BLAS by default,
which makes face encoding about 15x slower. howdy-compare links against
whichever BLAS was installed when it was built, so rebuild it if you remove
`blas-openblas` later, or face unlock stops and falls back to the password.

### Building from source

#### Dependencies

- Go, pkgconf, and dlib with its development headers
- ONNX Runtime (`onnxruntime-cpu` on Arch), or build with `-tags noonnx`
- To build the PAM module and howdy-gtk: meson, ninja, cmake, libinih,
  libevdev and pam, plus a Howdy checkout

#### Build

`packaging/arch` builds a package from this checkout, with the PAM module
and howdy-gtk from a Howdy checkout next to it (`../howdy`, or set
`HOWDY_REPO`):

    cd packaging/arch && makepkg -si

Only committed changes get built. To build just the Go programs:

    go build -o bin/ ./cmd/howdy ./cmd/howdy-compare

## Setup

After installation, howdy-go needs to learn what you look like so it can
recognise you later.

1. Find the IR camera with `v4l2-ctl --list-devices` and set `device_path`
   with `sudo howdy config`.
2. Run `sudo howdy add` to add a face model, sitting the way you normally do
   when you unlock. It averages 15 frames, so hold still for a few seconds.
   If you only sometimes wear glasses, add one model with them and one
   without.
3. Open a root shell (`sudo -i`) in another terminal and leave it open.
   Add `auth sufficient pam_howdy.so` at the top of `/etc/pam.d/sudo`, then
   run `sudo -k; sudo true` in a new terminal. If nothing went wrong, it
   lets you in by just showing your face. Once that works, add the same
   line wherever else you want it (`system-auth`, your screen locker, your
   display manager).

If something breaks, remove the PAM line from the root shell, or go back to
Python Howdy by installing `howdy` or `howdy-git` again.

`sudo howdy config` opens the central config file with all the options. On
most systems that's nano, where `ctrl`+`x` saves your changes.

## CLI

The `howdy` command manages face models for the current user, or the user
who ran `sudo`. Use `howdy --help` to list the options.

    howdy [-U user] [-y] [--plain] command [arguments...]

| command | what it does |
|---|---|
| `add [label]` | record a new face model |
| `list` | show your face models |
| `remove <id>` | delete one model |
| `clear` | delete all your models |
| `config` | open `config.ini` in your editor |
| `set <key> <value>` | change one config value |
| `disable 1`, `disable 0` | turn face unlock off or on for everyone |
| `test` | show every camera frame with its darkness and match, until ctrl+C |
| `snapshot` | save a picture from the camera |
| `version` | print the version |

## Face models

By default faces are found and recognised with dlib's 2017 models, like
Howdy. `recognizer = sface` under `[core]` switches to YuNet and SFace
(OpenCV Zoo, MIT and Apache-2.0) on ONNX Runtime. On one IR camera, sface
kept recognising its user with glasses on and off, which dlib at certainty
3.5 didn't, and it left a wider margin to other people. Under sface,
`match_threshold` under `[video]` replaces `certainty`: it defaults to
0.363, and higher is stricter. `howdy add` keeps writing dlib models too,
so you can switch back at any time. [docs/sface.md](docs/sface.md) covers
setup, how the models are stored and what was measured.

To compare models on your own camera, record a few sessions of yourself
(different days, glasses on and off), of other people with their consent,
and optionally of a photo or screen of you held up to the camera. Frames
stay in `~/.cache/howdy-go/faces`, mode 0700, and the report has scores,
never encodings.

    go build -o face-capture ./cmd/face-capture
    ./face-capture -device /dev/videoN $USER        # once per session
    ./face-capture -device /dev/videoN guest1
    ./face-capture -device /dev/videoN attack-phone
    tools/fetch-face-models                          # -r adds ArcFace, non-commercial
    python3 -I tools/face-eval.py --upsample 0 --certainty 3.5 --dark-threshold 100

Pass the `[video]` values you actually unlock with. Otherwise the harness
uses whatever `config.ini` said when you recorded.

## Troubleshooting

`sudo howdy test` prints every frame the camera gives, with its darkness and
match, and a summary when you press ctrl+C. Set `end_report = true` under
`[debug]` to get the timings and the winning match on each real unlock.

- Many frames before a match, with a winning certainty close to your
  `certainty` setting, means the face model doesn't fit how you sit. Enroll
  again (`sudo howdy clear`, then `sudo howdy add`) rather than raising
  `certainty`.
- If every other frame is dark, that's normal: a lot of IR emitters strobe.
  Those frames take about 12 ms each and don't slow anything down.
- If most frames count as dark even with the emitter on, raise
  `dark_threshold`.
- `required_matches` and `enroll_frames` aren't in upstream's `config.ini`.
  Add them under `[video]` with `sudo howdy config`, since `howdy set` only
  changes keys that are already there.

## Differences from Howdy

- **Stricter config parsing.** The PAM module and Python read `config.ini`
  differently in some corner cases, so howdy-go refuses files where they
  would disagree: duplicate keys or sections, sections that differ only in
  case, values containing ` ;`, `nan` or `inf`, `%` interpolation and
  invalid booleans.
- **Fails closed instead of crashing.** An unreadable model, a bad or
  incomplete config, or missing dlib data exits with 12 instead of a Python
  traceback, and PAM falls through to the password prompt.
- **Only shrinks frames.** Python also enlarges frames smaller than
  `max_height`, and howdy-go doesn't.
- **Drops root.** Under sudo, howdy-compare switches to `nobody` once the
  camera and models are open, so snapshots aren't saved there.
- **Always reads the camera through V4L2.** `recording_plugin` is ignored,
  and MJPEG-only cameras don't work.
- **No auth popup.** It can't start under PAM anyway.
- **`howdy test` is a terminal readout**, not a window.
- **Not done yet:** rubberstamps. With them enabled, howdy-compare refuses
  to run rather than skip them. x86_64 only.

## Contributing

| path | what |
|---|---|
| `cmd/howdy-compare` | what PAM runs: `howdy-compare <user>`, exit code is the result |
| `cmd/howdy` | the CLI |
| `cmd/save-frontal-detector` | build step that writes dlib's HOG detector to a file |
| `cmd/v4l2bench` | times opening the camera to the first frame |
| `cmd/face-capture` | records IR frames for `tools/face-eval.py`, into a private dir outside the repo |
| `internal/compare` | the frame loop, exit codes and end report |
| `internal/cli` | the commands behind `howdy` |
| `internal/config` | INI parser that reads config.ini the way the PAM module does |
| `internal/model` | face model JSON, byte-for-byte compatible with Python's `json.dump` |
| `internal/enroll` | averages the frames `howdy add` collects |
| `internal/dlib` | cgo wrapper around dlib's HOG detector, shape predictor and ResNet encoder |
| `internal/sface`, `internal/onnx` | the sface backend, and an ONNX Runtime wrapper that only loads files with a pinned sha256 |
| `internal/backend` | picks the configured recogniser and the stored encodings that go with it |
| `internal/imgproc` | CLAHE, the darkness histogram, resizing and warping, matched to OpenCV |
| `internal/v4l2`, `internal/camera` | camera access with plain V4L2 ioctls |
| `internal/snapshot` | JPEG snapshots of login attempts |
| `internal/harden` | resource limits, no_new_privs and dropping root |
| `packaging/arch`, `packaging/aur` | the local and AUR PKGBUILDs; the AUR one patches the PAM module with `pam-compare-path.patch` |

`go test ./...` runs everything that doesn't need face data or downloaded
models. `-tags "nodlib noonnx"` builds without dlib and ONNX Runtime. The
dlib wrapper is checked against python-dlib on public domain NASA portraits,
downloaded outside the repo:

    tools/fetch-test-images ~/.cache/howdy-go/testdata
    python3 -I tools/dlib-reference.py ~/.cache/howdy-go/testdata
    HOWDY_GO_TESTDATA=~/.cache/howdy-go/testdata go test -run Parity ./internal/dlib

## A note on security

Like Howdy, this is in no way as secure as a password, and never will be.
Someone who looks like you, or a good photo or screen of you, could be
enough to fool it. There is no liveness check yet. It's a quicker way of
logging in, not a safer one.

Keep the PAM module in `/lib/security` and the installed files read-only,
and keep `auth sufficient`, so the password still works when face unlock
doesn't.

**DO NOT USE HOWDY-GO AS THE SOLE AUTHENTICATION METHOD FOR YOUR SYSTEM.**

## Licence

MIT, see [LICENSE](LICENSE). Howdy is MIT too, by boltgolt.
