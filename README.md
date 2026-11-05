# howdy-go

Windows Hello™ style face unlock for Linux. Your IR camera and face
recognition sign you in wherever PAM would ask for your password: login,
lock screen, sudo, su.

It is [Howdy](https://github.com/boltgolt/howdy) with its Python half
rewritten in Go: `howdy-compare`, which PAM runs on every unlock, and the
`howdy` command. It reads and writes Howdy's own files (`config.ini`,
`/etc/howdy/models/<user>.dat`, snapshots), so existing face models keep
working, and the PAM module and howdy-gtk are Howdy's. The point is speed:
the camera opens while the models load, so `sudo true` with a face match
takes about 460 ms on a 15 fps IR camera, against 790 ms for Python Howdy.

> **Convenience, not security.** Like Howdy, this is nowhere near as secure
> as a password. Someone who looks like you, or a good photo or screen of
> you, may be enough to get in, and there is no liveness check yet. Keep
> `auth sufficient` so your password always still works, and
> **don't use howdy-go as the only way into your system.**

## Installation

**Arch Linux:** the `howdy-go-git` AUR package. It replaces `howdy` or
`howdy-git` and builds the PAM module and howdy-gtk from upstream Howdy at
a pinned commit.

    git clone https://aur.archlinux.org/howdy-go-git.git
    cd howdy-go-git && makepkg -si

Install `blas-openblas` too: Arch's dlib defaults to the reference BLAS,
which makes face encoding about 15x slower. howdy-compare links against the
BLAS installed when it was built, so rebuild it if you remove
`blas-openblas` later.

**From source** you need Go, pkgconf, dlib with its headers and ONNX
Runtime (`onnxruntime-cpu` on Arch, or build with `-tags noonnx`). The PAM
module and howdy-gtk also need meson, ninja, cmake, libinih, libevdev, pam
and a Howdy checkout. `packaging/arch` packages the committed state of this
checkout, with Howdy from `../howdy` (or `HOWDY_REPO`):

    cd packaging/arch && makepkg -si

For just the Go programs: `go build -o bin/ ./cmd/howdy ./cmd/howdy-compare`.

## Setup

1. Find the IR camera with `v4l2-ctl --list-devices` and set `device_path`
   with `sudo howdy config`.
2. Run `sudo howdy add`, sitting the way you normally do, and hold still for
   a few seconds. If you only sometimes wear glasses, add one model with
   them and one without.
3. Open a root shell (`sudo -i`) and leave it open. Add
   `auth sufficient pam_howdy.so` at the top of `/etc/pam.d/sudo`, then run
   `sudo -k; sudo true` in a new terminal. Once that works, add the same
   line wherever else you want it (`system-auth`, your screen locker, your
   display manager).

If something breaks, remove the PAM line from the root shell, or install
`howdy` or `howdy-git` again to go back to Python Howdy.

## Options

`sudo howdy config` opens `/etc/howdy/config.ini`. The ones you're most
likely to change:

| option | default | what it does |
|---|---|---|
| `[core] recognizer` | `dlib` | `dlib` (Howdy's 2017 models) or `sface` (newer, see below) |
| `[video] certainty` | 3.5 | dlib: the highest distance that still matches, lower is stricter |
| `[video] match_threshold` | 0.363 | sface: the lowest similarity that still matches, higher is stricter |
| `[video] required_matches` | 1 | matching frames needed; 2 stops a single lucky frame from someone else |
| `[video] timeout` | 4 | seconds to look for your face |
| `[video] device_path` | | the IR camera |
| `[video] dark_threshold` | 60 | raise it if most frames are skipped as dark |
| `[video] detection_upsample` | 1 | 0 is several times faster and enough when you sit close |
| `[video] enroll_frames` | 15 | frames `howdy add` averages into a model |
| `[snapshots] save_failed`, `save_successful` | false | save a picture of each attempt |
| `[debug] end_report` | false | print timings and the winning match after each unlock |

Keys missing from the file use the default. `howdy set` only changes keys
that are already there, so add new ones with `sudo howdy config`.

### Recognisers

`dlib` is the default, with the same models as Howdy. `sface` uses YuNet and
SFace from OpenCV Zoo (MIT and Apache-2.0) on ONNX Runtime. On one IR
camera it kept recognising its user with glasses on and off, which dlib
didn't, and left a wider margin to other people. To switch, set
`recognizer = sface` and run `sudo howdy add` again. `howdy add` keeps
writing dlib models too, so you can switch back at any time.
[docs/sface.md](docs/sface.md) covers how it's stored, what was measured and
how to compare the models on your own camera.

## CLI

`howdy [-U user] [-y] [--plain] command [arguments...]` manages face models
for the current user, or the user who ran `sudo`.

| command | what it does |
|---|---|
| `add [label]` | record a new face model |
| `list` | show your face models |
| `remove <id>`, `clear` | delete one model, or all of them |
| `config` | open `config.ini` in your editor |
| `set <key> <value>` | change one config value |
| `disable 1`, `disable 0` | turn face unlock off or on for everyone |
| `test` | show every camera frame with its darkness and match, until ctrl+C |
| `snapshot` | save a picture from the camera |
| `version` | print the version |

## Troubleshooting

Start with `sudo howdy test`, or set `end_report = true` to see the timings
and winning match on each real unlock.

- **Many frames before a match**, close to your threshold: the model doesn't
  fit how you sit. Enroll again (`sudo howdy clear`, `sudo howdy add`)
  rather than loosening `certainty` or `match_threshold`.
- **Every other frame dark:** normal, a lot of IR emitters strobe. Those
  frames cost about 12 ms each.
- **Most frames dark** even with the emitter on: raise `dark_threshold`.

## Differences from Howdy

- **Stricter config parsing.** The PAM module and Python disagree on some
  `config.ini` corner cases (duplicate keys or sections, sections differing
  only in case, ` ;` in values, `nan`/`inf`, `%` interpolation, invalid
  booleans), so howdy-go refuses those files.
- **Fails closed.** A bad config or model, or missing model data, exits 12
  instead of crashing, and PAM falls back to the password.
- **Drops root** once the camera and models are open, so snapshots aren't
  saved under sudo.
- **V4L2 only.** `recording_plugin` is ignored and MJPEG-only cameras don't
  work. Frames are only ever shrunk to `max_height`, never enlarged.
- **No auth popup**, and `howdy test` is a terminal readout, not a window.
- **Not done yet:** rubberstamps (howdy-compare refuses to run with them
  enabled). x86_64 only.

## Contributing

| path | what |
|---|---|
| `cmd/howdy-compare`, `internal/compare` | what PAM runs: the frame loop, exit codes and end report |
| `cmd/howdy`, `internal/cli` | the CLI |
| `cmd/face-capture`, `tools/face-eval.py` | record IR frames and compare models on them |
| `cmd/save-frontal-detector`, `cmd/v4l2bench` | build step for dlib's HOG detector; camera start-up timer |
| `internal/config` | INI parser that reads `config.ini` the way the PAM module does |
| `internal/model`, `internal/enroll` | face model JSON, byte for byte like Python's; averaging `howdy add` frames |
| `internal/backend`, `internal/dlib`, `internal/sface`, `internal/onnx` | the recognisers, and an ONNX Runtime wrapper that only loads files with a pinned sha256 |
| `internal/imgproc`, `internal/v4l2`, `internal/camera`, `internal/snapshot` | image processing matched to OpenCV, camera access, snapshots |
| `internal/harden` | resource limits, no_new_privs and dropping root |
| `packaging/arch`, `packaging/aur` | local and AUR PKGBUILDs; the AUR one patches the PAM module with `pam-compare-path.patch` |

`go test ./...` runs every test that doesn't need face data or downloaded
models; `-tags "nodlib noonnx"` builds without dlib and ONNX Runtime. To
also check the dlib wrapper against python-dlib on public domain NASA
portraits, kept outside the repo:

    tools/fetch-test-images ~/.cache/howdy-go/testdata
    python3 -I tools/dlib-reference.py ~/.cache/howdy-go/testdata
    HOWDY_GO_TESTDATA=~/.cache/howdy-go/testdata go test -run Parity ./internal/dlib

## Licence

MIT, see [LICENSE](LICENSE). Howdy is MIT too, by boltgolt.
