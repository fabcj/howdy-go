# howdy-go

Face unlock for Linux, in the style of Windows Hello. Your IR camera and
face recognition sign you in wherever PAM would ask for a password: the
login screen, the lock screen, sudo, and su.

howdy-go is [Howdy](https://github.com/boltgolt/howdy) with its Python half
rewritten in Go. The Go parts are `howdy-compare`, which PAM runs on every
unlock, and the `howdy` command. They read and write Howdy's own files
(`config.ini`, `/etc/howdy/models/<user>.dat`, snapshots), so your existing
face models keep working. The PAM module and howdy-gtk are still Howdy's.

The main reason for it is speed. The camera opens while the models load, so
`sudo true` with a face match takes about 460 ms on a 15 fps IR camera.
Python Howdy takes 790 ms on the same kind of setup. It also avoids a lot of
the dependency errors that the original Howdy runs into.

> **Convenience, not security.** Like Howdy, this is nowhere near as secure
> as a password. Someone who looks like you, or a good photo or screen of
> you, may be enough to get in. There is no liveness check yet. Keep
> `auth sufficient` so your password always works, and
> **don't use howdy-go as the only way into your system.**

## Installation

**Arch Linux:** install the `howdy-go-git` AUR package. It replaces `howdy`
or `howdy-git`, and it builds the PAM module and howdy-gtk from upstream
Howdy at a pinned commit.

    git clone https://aur.archlinux.org/howdy-go-git.git
    cd howdy-go-git && makepkg -si

Install `blas-openblas` too. Arch's dlib uses the reference BLAS by default,
and that makes face encoding about 15 times slower. howdy-compare links
against whichever BLAS was installed when it was built, so rebuild it if you
remove `blas-openblas` later.

**From source** you need Go, pkgconf, dlib with its headers, and ONNX
Runtime (`onnxruntime-cpu` on Arch, or build with `-tags noonnx`). The PAM
module and howdy-gtk also need meson, ninja, cmake, libinih, libevdev, pam,
and a Howdy checkout. `packaging/arch` packages the committed state of this
checkout. It takes Howdy from `../howdy`, or from `HOWDY_REPO` if you set it:

    cd packaging/arch && makepkg -si

If you only want the Go programs, run
`go build -o bin/ ./cmd/howdy ./cmd/howdy-compare`.

## Setup

1. Find the IR camera with `v4l2-ctl --list-devices`, then set `device_path`
   with `sudo howdy config`.
2. Run `sudo howdy add`, sit the way you normally do, and hold still for a
   few seconds. If you only sometimes wear glasses, add one model with them
   and one without.
3. Open a root shell with `sudo -i` and leave it open. Add
   `auth sufficient pam_howdy.so` at the top of `/etc/pam.d/sudo`, then run
   `sudo -k; sudo true` in a new terminal. Once that works, add the same line
   anywhere else you want it: `system-auth`, your screen locker, or your
   display manager.

If something breaks, remove the PAM line from the root shell. You can also
install `howdy` or `howdy-git` again to go back to Python Howdy.

## Options

`sudo howdy config` opens `/etc/howdy/config.ini`. These are the options
you're most likely to change:

| option | default | what it does |
|---|---|---|
| `[core] recognizer` | `dlib` | `dlib` (Howdy's 2017 models) or `sface` (newer, see below) |
| `[video] certainty` | 3.5 | dlib: the largest distance that still counts as a match. Lower is stricter. |
| `[video] match_threshold` | 0.363 | sface: the lowest similarity that still counts as a match. Higher is stricter. |
| `[video] required_matches` | 1 | matching frames needed. 2 stops one lucky frame from letting someone else in. |
| `[video] timeout` | 4 | seconds to look for your face |
| `[video] device_path` | | the IR camera |
| `[video] dark_threshold` | 60 | raise it if most frames are skipped as dark |
| `[video] detection_upsample` | 1 | 0 is several times faster, and it's enough when you sit close |
| `[video] enroll_frames` | 15 | frames `howdy add` averages into a model |
| `[snapshots] save_failed`, `save_successful` | false | save a picture of each attempt |
| `[debug] end_report` | false | print timings and the winning match after each unlock |

Keys missing from the file use the default. `howdy set` only changes keys
that are already in the file, so add new ones with `sudo howdy config`.

### Recognisers

`dlib` is the default, and it uses the same models as Howdy. `sface` uses
YuNet and SFace from OpenCV Zoo (MIT and Apache-2.0) on ONNX Runtime. On one
IR camera, it kept recognising its user with glasses on and off, which dlib
didn't. It also left a wider gap between the user and other people. To
switch, set `recognizer = sface` and run `sudo howdy add` again. `howdy add`
still writes dlib models too, so you can switch back at any time.
[docs/sface.md](docs/sface.md) covers how sface data is stored, what was
measured, and how to compare the two models on your own camera.

### A PIN after the face scan

A face match alone could be someone who looks like you, or a good photo.
With the `pin` argument, `pam_howdy.so` also asks for a 4 digit PIN after
the face matches. That way, getting in takes both.

    sudo howdy pin set            # asks twice, typed without echo
    # then in /etc/pam.d/sudo:
    auth sufficient pam_howdy.so pin

The PIN is asked for up to 3 times per scan. The scan ends early if the
program asking gives the same answer twice (see sddm below). A wrong PIN
never unlocks. After 3 tries, or if there is no PIN, or the PIN file isn't
safe to trust, or the PIN has been switched off, it falls back to your
password. A failed face scan never asks for a PIN. The PIN is a choice for
each service, so `sudo` can ask for one while a line without `pin` doesn't.

What it protects against: someone at your keyboard who passes the face
check without knowing the PIN. Here is where it stops:

- **Guessing.** 4 digits make 10,000 possible values, so the limit on wrong
  tries is what protects them. 3 wrong tries end a scan. 6 wrong PINs in a
  row, across any number of scans, switch the PIN off until
  `sudo howdy pin set` turns it back on. Only a right PIN resets the count.
  Until then, that service asks for your password, which always works.
  Answers that aren't 4 digits don't count.
- **The hash.** It's a yescrypt hash in a root-only directory. Still, anyone
  who can read it can crack 4 digits. That's fine for `sudo`, because anyone
  who can read it is already root. PAM runs as root for `sudo` and for login
  managers such as sddm, so those use this PIN.
- **Anything that can watch your keyboard.** A keylogger can capture a PIN
  typed on a keyboard. If your user is in the `input` group, any program
  running as you can read every key, including your password and PIN,
  without root. Check with `id`, and drop the group if nothing needs it. A
  face match and the PIN are both needed at the moment of use, so this only
  helps an attacker who also gets past the face check.
- **sudo's own cache.** After a successful `sudo`, anything on that terminal
  can run `sudo` again with no face and no PIN until sudo's timestamp runs
  out (5 minutes by default). `Defaults timestamp_timeout=0` closes that gap.

`howdy pin clear` removes the PIN. `howdy pin status` says whether there is
one, and how many wrong tries it has had. Easy PINs (1111, 1234, 1212) are
refused.

**Lock screens** (hyprlock, swaylock) run PAM as you, so they can't read the
root-only PIN. With `pin`, they would fall back to the password. Instead,
they have their own PIN, set with the `pin-user` argument:

    sudo howdy pin set lock-screen
    # then in /etc/pam.d/hyprlock:
    auth sufficient pam_howdy.so pin-user

Its files belong to you, in `/etc/howdy/pin-user`, and only root can change
that directory. Since anything running as you can read it, this guards
against someone at your locked laptop, which is what a lock screen is for.
It doesn't guard against malware already running in your session. It has
the same 3 tries and 6 in a row. `pin-user` is refused when PAM runs as root,
and for any user other than the one the process runs as. That stops it from
being used to get around the root-only PIN. Don't put `pin-user` on `sudo`
or a login manager.

**Login managers.** A greeter that can ask a second question shows the PIN
prompt like any other. sddm can't. It answers every prompt with whatever is
in its password box. With `workaround = off`, type the PIN in that box and
press Enter while looking at the camera. The face scan runs first, and the
PIN is checked after it. A wrong PIN counts once, not three times. If the
scan fails, the PIN is never checked. The text in the box, which is still
your PIN, goes to the password prompt and fails there. So type your password
and press Enter again. Greeters that only ever show one password field work
the same way as sddm. With the other `workaround` settings, howdy's own
password prompt takes what you type, so the PIN can't be typed there.

**`workaround`** in `[core]` (`off`, `input` or `native`) controls whether
Howdy shows a password prompt next to the face scan. `off` is the simplest:
the face scan runs, then the password prompt if it failed. `input` needs
write access to `/dev/uinput` so it can press Enter for you. `native` cancels
the prompt's thread, which Howdy itself calls unsafe. The PIN works with all
three, but `off` is the one with nothing extra to trust.

### Regular cameras

howdy-go is built and measured for IR cameras. A regular colour webcam works
if it offers `YUYV` (or `GREY`). Cameras that only offer `MJPG` don't work,
and the error message lists what the camera does offer. Everything else is
untested on colour cameras:

- **Thresholds.** `certainty`, `match_threshold`, and sface's detection score
  cutoff (0.85) were set on one IR camera. A photo or screen of you looks
  much more like you in colour light than under IR, so expect it to be easier
  to fool. Frames are reduced to grey either way, and nothing checks that a
  face is live.
- **Warning.** When a `YUYV` camera sends colour, `howdy test`, `howdy add`
  and `howdy snapshot` print a warning on stderr. The warning is a guess based
  on the colour in the frames, so a colour camera in a very grey scene can get
  past it. Nothing detects a `GREY` camera that isn't IR.
- **If you try one anyway:** set `required_matches = 2`, set `frame_width`
  and `frame_height` (`YUYV` at high resolutions is slow), keep
  `auth sufficient` so your password still works, and check it with
  `tools/face-eval.py` on frames of you, other people, and a photo of you
  ([docs/sface.md](docs/sface.md)) before you rely on it.

## CLI

`howdy [-U user] [-y] [--plain] command [arguments...]` manages face models
for the current user, or for the user who ran `sudo`.

| command | what it does |
|---|---|
| `add [label]` | record a new face model |
| `list` | show your face models |
| `remove <id>`, `clear` | delete one model, or all of them |
| `config` | open `config.ini` in your editor |
| `set <key> <value>` | change one config value |
| `disable 1`, `disable 0` | turn face unlock off or on for everyone |
| `test` | show every camera frame with its darkness and match, until you press ctrl+C |
| `pin set`, `pin clear`, `pin status` | the PIN asked for after a face match, see above |
| `snapshot` | save a picture from the camera |
| `version` | print the version |

## Troubleshooting

Start with `sudo howdy test`, or set `end_report = true` to see the timings
and the winning match on each real unlock.

- **Many frames before a match, close to your threshold:** the model doesn't
  fit how you sit. Enroll again (`sudo howdy clear`, then `sudo howdy add`)
  instead of loosening `certainty` or `match_threshold`.
- **Every other frame dark:** that's normal. A lot of IR emitters strobe.
  Those dark frames cost a millisecond or two each
  ([docs/performance.md](docs/performance.md)).
- **Most frames dark** even with the emitter on: raise `dark_threshold`.

## Differences from Howdy

- **Stricter config parsing.** The PAM module and the Python code disagree on
  some `config.ini` edge cases: duplicate keys or sections, sections that
  differ only in case, ` ;` inside values, `nan` and `inf`, `%` interpolation,
  and invalid booleans. howdy-go refuses those files.
- **Fails closed.** A bad config, a bad model, or missing model data exits
  with code 12 instead of crashing. PAM then falls back to the password.
- **Drops root** to the `howdy` user once the camera and models are open, so
  snapshots aren't saved under sudo. Then it locks the filesystem with
  Landlock for the rest of the scan.
- **Model files** are owned by root, and their one user can read them through
  an ACL. The user's whole primary group can't read them. So `/etc/howdy`
  needs a filesystem with ACLs (any of the usual ones).
- **V4L2 only.** `recording_plugin` is ignored, and MJPEG-only cameras don't
  work. Frames are only ever shrunk to `max_height`, never enlarged.
- **No auth popup.** `howdy test` prints to the terminal instead of opening
  a window.
- **Not done yet:** rubberstamps (howdy-compare refuses to run with them
  enabled). x86_64 only.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for building, checks, and releases.
See [SECURITY.md](SECURITY.md) to report a vulnerability. Here's what lives
where:

| path | what |
|---|---|
| `cmd/howdy-compare`, `internal/compare` | what PAM runs: the frame loop, exit codes, and the end report |
| `cmd/howdy`, `internal/cli` | the CLI |
| `cmd/face-capture`, `tools/face-eval.py` | record IR frames and compare models on them |
| `cmd/save-frontal-detector`, `cmd/v4l2bench` | build step for dlib's HOG detector; camera start-up timer |
| `internal/config` | INI parser that reads `config.ini` the way the PAM module does |
| `internal/model`, `internal/enroll` | face model JSON, byte for byte like Python's; averaging `howdy add` frames |
| `internal/backend`, `internal/dlib`, `internal/sface`, `internal/onnx` | the recognisers, and an ONNX Runtime wrapper that only loads files with a pinned sha256 |
| `internal/imgproc`, `internal/v4l2`, `internal/camera`, `internal/snapshot` | image processing matched to OpenCV, camera access, snapshots |
| `internal/harden` | resource limits, no_new_privs, dropping root, and the Landlock lockdown |
| `internal/pin` | the PIN's yescrypt hash and its root-only file; the PAM module (`pam-pin.patch`) checks it with the same libcrypt |
| `packaging/arch`, `packaging/aur/howdy-go-git`, `packaging/aur/howdy-go` | the local, AUR `-git`, and AUR release PKGBUILDs; the AUR ones patch the PAM module with `pam-compare-path.patch` |

CI (`.github/workflows/ci.yml`) runs gofmt, vet, staticcheck, govulncheck,
golangci-lint, and the tests, with and without dlib and ONNX Runtime. It
builds the AUR package every week and on pushes to main. `go test ./...`
runs every test that doesn't need face data or downloaded models.
`-tags "nodlib noonnx"` builds without dlib and ONNX Runtime. To also check
the dlib wrapper against python-dlib, use the public domain NASA portraits.
They're kept outside the repo:

    tools/fetch-test-images ~/.cache/howdy-go/testdata
    python3 -I tools/dlib-reference.py ~/.cache/howdy-go/testdata
    HOWDY_GO_TESTDATA=~/.cache/howdy-go/testdata go test -run Parity ./internal/dlib

## Licence

MIT, see [LICENSE](LICENSE). Howdy is MIT too, by boltgolt.
