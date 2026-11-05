# howdy-go

A Go rewrite of the Python half of [Howdy](https://github.com/boltgolt/howdy),
face unlock for Linux: the `howdy-compare` program that PAM runs on every
unlock, and the `howdy` command. The point is speed. The Python version
spends about 320 ms starting the interpreter and importing numpy, OpenCV and
dlib before it looks at a single frame, and the GIL keeps the camera and the
model loading from overlapping.

It reads and writes the same files as Howdy (`/etc/howdy/config.ini`,
`/etc/howdy/models/<user>.dat`, the dlib model files, snapshots), so
existing face models keep working and howdy-gtk still works on top of it.

The PAM module stays Howdy's C++ one. A small patch makes it run
`howdy-compare` directly instead of `python compare.py`.

On a laptop with a 15 fps IR camera, `sudo true` with a face match takes
about 460 ms, against 790 ms for Python Howdy on the same machine. Camera
start and model loading overlap, and both are done in about 145 ms.
[docs/parity.md](docs/parity.md) describes what it has to match
and where it deliberately doesn't.

## Install

From the AUR, as `howdy-go-git`. It replaces `howdy` or `howdy-git`, and
builds the PAM module and howdy-gtk from upstream Howdy at a pinned commit.

    git clone https://aur.archlinux.org/howdy-go-git.git
    cd howdy-go-git && makepkg -si

Install `blas-openblas` too. Arch's dlib uses the reference BLAS by default,
which makes face encoding about 15x slower. howdy-compare links against
whichever BLAS was installed when it was built, so rebuild it if you remove
`blas-openblas` later, or face unlock stops and falls back to the password.

Coming from Howdy, your face models keep working as they are, and so does
`config.ini` unless it has something the stricter parser refuses, like a
duplicate key (Deviations in the spec has the list). On a fresh install:

1. Find the IR camera with `v4l2-ctl --list-devices` and set `device_path`
   with `sudo howdy config`.
2. Add your face with `sudo howdy add`, sitting the way you normally do when
   you unlock. It averages 15 frames, so hold still for a few seconds.
3. Open a root shell (`sudo -i`) in another terminal and leave it open.
   Add `auth sufficient pam_howdy.so` at the top of `/etc/pam.d/sudo` and
   try `sudo -k; sudo true`. Once that works, add it to whatever else you
   want (`system-auth`, your screen locker, your display manager).

If something breaks, remove the PAM line from the root shell, or go back to
Python Howdy by installing `howdy` or `howdy-git` again.

## Slow or failed unlocks

`sudo howdy test` prints every frame the camera gives, with its darkness
and certainty, and a summary when you press ctrl+C. Set `end_report = true`
under `[debug]` to get the timings and the winning certainty on each real
unlock.

- Many frames before a match, with a winning certainty close to your
  `certainty` setting, means the face model doesn't fit how you sit. Enroll
  again (`sudo howdy clear`, then `sudo howdy add`) rather than raising
  `certainty`.
- Every other frame dark is normal, a lot of IR emitters strobe. Those
  frames take about 12 ms each and don't slow anything down.
- `required_matches` and `enroll_frames` aren't in upstream's `config.ini`.
  Add them under `[video]` with `sudo howdy config`, since `howdy set` only
  changes keys that are already there.

## Layout

| path | what |
|---|---|
| `cmd/howdy-compare` | what PAM runs: `howdy-compare <user>`, exit code is the result |
| `cmd/howdy` | the CLI: add, remove, list, clear, config, disable, set, snapshot, test, version |
| `cmd/save-frontal-detector` | build step that writes dlib's HOG detector to a file |
| `cmd/v4l2bench` | times opening the camera to the first frame |
| `internal/compare` | the frame loop, exit codes and end report |
| `internal/cli` | the commands behind `howdy` |
| `internal/config` | INI parser that reads config.ini the same way the PAM module does |
| `internal/model` | face model JSON, byte-for-byte compatible with Python's `json.dump` |
| `internal/enroll` | averages the frames `howdy add` collects |
| `internal/v4l2`, `internal/camera` | camera access with plain V4L2 ioctls, no cgo |
| `internal/dlib` | small cgo wrapper around dlib's HOG detector, shape predictor and ResNet encoder |
| `internal/imgproc` | CLAHE, the darkness histogram and resizing, matched to OpenCV |
| `internal/snapshot` | JPEG snapshots of login attempts |
| `internal/harden` | resource limits, no_new_privs and dropping root |
| `packaging/arch` | PKGBUILD for a local build against a Howdy checkout |
| `packaging/aur` | the AUR PKGBUILD, upstream Howdy plus `pam-compare-path.patch` |

## Not done yet

- Rubberstamps. With them enabled, `howdy-compare` refuses to run
  (exit 12) rather than skip them.
- The `recording_plugin` option is ignored: the camera is always read
  through V4L2, so MJPEG-only cameras don't work.
- x86_64 only, since that's all it has been tested on.

## Testing

`go test ./...` runs everything that doesn't need dlib (add `-tags nodlib`
to build without it). Several tests compare against Python, numpy and
OpenCV when they're installed.

The dlib wrapper is checked against python-dlib on public domain NASA
portraits, downloaded outside the repo:

    tools/fetch-test-images ~/.cache/howdy-go/testdata
    python3 -I tools/dlib-reference.py ~/.cache/howdy-go/testdata
    HOWDY_GO_TESTDATA=~/.cache/howdy-go/testdata go test -run Parity ./internal/dlib

## Licence

MIT, see [LICENSE](LICENSE). Howdy is MIT too, by boltgolt.
