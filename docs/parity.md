# Parity spec

What `howdy-compare` and `howdy` have to do to stand in for Howdy's Python
code. The reference is Howdy's master branch (3.0.0) plus a set of patches
on top of it: a plain V4L2 recorder, loading the HOG detector from a saved
`.svm`, `detection_upsample`, averaged enrollment, `required_matches`, and
safer snapshot and model writes. Files named here are relative to
`howdy/src/` in that tree.

This describes what the Python code actually does, bugs included. Where Go
does something else, it's marked **Change** and listed again under
[Deviations](#deviations) at the end.

`<user>` is the user being authenticated, `/dev/videoN` the configured
camera.

## 1. Process contract

### Spawn

`pam/main.cc` runs

    posix_spawnp(PYTHON_EXECUTABLE_PATH, [PYTHON_EXECUTABLE_PATH, COMPARE_PROCESS_PATH, username], envp = NULL)

- **The child's environment is empty.** glibc passes the NULL envp straight
  to execve, and Linux treats that as an empty environment (checked with a
  C probe that spawns `/usr/bin/env`, which printed nothing). There's no
  PATH, LANG, TZ, HOME, DISPLAY or WAYLAND_DISPLAY. Consequences:
  - translations never apply under PAM (and no `core` .mo files are
    installed anyway), so all output is the English source strings;
  - `OPENBLAS_NUM_THREADS` from the parent never reaches the child, which is
    why compare.py sets it in `os.environ` itself before importing dlib;
  - the howdy-gtk auth popup can't start (see 4.9).
- If stdout or stderr is a socket (polkit's agent helper), PAM reopens it on
  `/dev/null` first (#1146).
- `argv[1]` is the PAM username, which isn't validated anywhere on the
  Python side. Before spawning, PAM has already returned
  `PAM_AUTHINFO_UNAVAIL` if `stat(USER_MODELS_DIR + "/" + username + ".dat")`
  fails, so compare rarely sees a user without a model file.
- PAM sends `SIGTERM` to the child when the password wins the race (any
  `workaround` other than `off`). Python dies on the signal and nothing
  cleans up. The kernel closes the camera fd.

**New contract for Go** (a small patch to the PAM module):

    posix_spawn(COMPARE_PATH, [COMPARE_PATH, username], envp = {"OPENBLAS_NUM_THREADS=1", "OMP_NUM_THREADS=1", NULL})

It uses a fixed envp and doesn't inherit `environ`, so nothing from the
calling process (sudo, hyprlock, polkit) reaches the compare process. It
also uses `posix_spawn`, not `spawnp`: the path is absolute and there's no
PATH to search. The meson option `python_path` is replaced by
`compare_path`. The socket redirect stays as it is.

### Exit codes

From `pam/main.hh`, `CompareError`. What PAM does with each one is in
`howdy_error()`:

| code | name | PAM shows the user | PAM syslogs (LOG_AUTHPRIV) |
|---|---|---|---|
| 0 | success | "Identified face as {}" unless `no_confirmation` | "Login approved" |
| 10 | NO_FACE_MODEL | nothing | "Failure, no face model known" |
| 11 | TIMEOUT_REACHED | "Failure, timeout reached" | same |
| 12 | ABORT | nothing | "Failure, general abort" |
| 13 | TOO_DARK | "Face detection image too dark" | "Failure, image too dark" |
| 14 | INVALID_DEVICE | nothing | "Failure, not possible to open camera at configured path" |
| 15 | RUBBERSTAMP | "Unknown error: " + garbage (see below) | "Failure, unknown error 15" |
| other | | "Unknown error: " + garbage | "Failure, unknown error %d" |
| signal | | nothing | "Child killed by signal %s (%d)" |

The C++ builds the message as `std::string(S("Unknown error: ") + status)`,
which adds the int to the `char *` (pointer arithmetic), not to the string.
So the user sees the message cut short by `status` characters. With 15 that's
an empty string. It's a PAM bug, fixed in the same PAM patch.

Every path that ends compare.py today:

| when | Python exit | Go exit |
|---|---|---|
| no `argv[1]` | 12 | 12 |
| model file doesn't exist | 10 | 10 |
| model file unreadable (EACCES, EISDIR...) | 1, traceback | **Change:** 12 |
| model file isn't valid JSON, or an entry has no `data` | 1, traceback | **Change:** 12 |
| model file holds `[]` | 10 | 10 |
| every model's `data` is `[]` | 1, traceback once a face is found (argmin of empty) | **Change:** 10 |
| config.ini unparseable (duplicate key or section, no header) | 1, traceback | **Change:** 12 |
| a config value the getter can't parse (`certainty = 3.5 # x`, `timeout = 4.0`) | 1, traceback | **Change:** 12 |
| `[video] device_path` missing | 1, traceback (NoOptionError) | **Change:** 12 |
| dlib data missing (5-point predictor not a file) | prints the install hint, then `sys.exit(1)` inside the loader thread, which only ends that thread. The main thread then crashes on `None(...)` with exit 1, or times out first if every frame is dark | **Change:** 12, after printing the same hint |
| `device_path` doesn't exist | 14, hint printed if `warn_no_device` | 14, same |
| camera can't be opened or configured | v4l2 falls back to OpenCV, which may still work | 14, no fallback |
| no frame within 2 s (`select` timeout) or read error | 14, "Failed to read camera..." | 14, same message |
| time since the first frame > `timeout`, and every valid frame was dark (this includes no valid frames at all) | 13, two messages | 13, same |
| time since the first frame > `timeout`, otherwise | 11 | 11 |
| match | 0 | 0 |
| match, then a rubberstamp fails | 15 | see 4.10 |
| privilege drop didn't take (new) | | 12 |
| SIGTERM | killed by signal | killed by signal |

Exit 1 shows up in PAM as "Unknown error" plus a mangled string. With 12,
PAM falls through to the password prompt without printing anything, which
is the right outcome for "compare couldn't run".

## 2. Paths

Fixed at build time (`paths.py.in`, `pam/paths.hh.in`). Arch values:

| name | value | used for |
|---|---|---|
| config file | `/etc/howdy/config.ini` | compare, CLI, PAM |
| user models dir | `/etc/howdy/models` | `<user>.dat` |
| dlib data dir | `/usr/share/dlib-data` | `shape_predictor_5_face_landmarks.dat`, `mmod_human_face_detector.dat`, `dlib_face_recognition_resnet_model_v1.dat`, `frontal_face_detector.svm` |
| log path | `/var/log/howdy` | `snapshots/` below it |
| data dir | `/usr/share/howdy` | `logo.png` for snapshots |

Go sets these with `-ldflags -X` from the PKGBUILD. Tests point them at a
scratch tree.

Expected permissions: `/etc/howdy/models` is `0755 root:root`, and each
model is `0640 root:<user's group>`, since lock screens such as hyprlock run
compare as the user. For their failed attempts to be saved,
`/var/log/howdy/snapshots` has to be writable by the user's group.

## 3. config.ini

### How the two parsers differ

compare.py and the CLI use Python's `configparser.ConfigParser()` with
default options. PAM uses inih's `INIReader` (libinih 62). Checked with a
probe of each on the same files:

| input | configparser | INIReader |
|---|---|---|
| missing file | empty config, no error | `ParseError() == -1`, so PAM returns `PAM_SYSTEM_ERR` and never spawns compare |
| `key = v # x` | value `v # x` | value `v # x` |
| `key = v ; x` | value `v ; x` | value `v` (inline `;` needs whitespace before it) |
| `key = 60;x` | `60;x` | `60;x` |
| `key: v` | ok | ok |
| `Key` vs `key` | keys lowercased | keys lowercased |
| `[Video]` vs `[video]` | different sections | same section |
| duplicate key in a section | DuplicateOptionError, read aborts | values joined with `\n` |
| duplicate section | DuplicateSectionError | merged |
| line before any section | MissingSectionHeaderError | accepted, section `""` |
| indented continuation line | appended with `\n`; a value that starts empty begins with `\n` | appended with `\n`, no leading `\n` |
| `%` in a value | `get` raises InterpolationSyntaxError (`50%`); `%(a)s` interpolates | literal |
| booleans | `1 yes true on` / `0 no false off`, any case, else ValueError | same words, any case, else the default |
| ints | `int()`: `+4`, ` 4` ok; `4.0`, `0x10`, `1e1` raise | `strtol(base 0)`: `0x10` = 16, `4.0` = 4, `3,5` = 3 |
| floats | `float()`: `1e1`, `inf`, `nan` ok; `3,5` raises | `strtod` prefix: `3,5` = 3 |
| empty value | `getint`/`getfloat` raise | default |

The PAM module only reads `[core]` booleans and `workaround`. compare and
the CLI read everything else, so the two only disagree on the values PAM
reads.

### What Go does

One strict parser for everything Go reads, which accepts a file only where
both parsers would agree:
- comments are lines whose first non-blank character is `#` or `;`;
- `key = value` or `key: value`, keys lowercased, values trimmed;
- continuation lines supported (`stamp_rules` uses them);
- **refuse the whole file** (compare exits 12, the CLI prints the line and
  exits 1) on a duplicate key, a duplicate section, a section name that only
  differs in case from another, or a line outside any section;
- **refuse a value** Go reads if it contains whitespace followed by `;`,
  because PAM would see something else there. `#` inside a value isn't a
  comment to either parser, so it just fails to parse as a number;
- numbers follow Python: ints are base 10 with an optional sign; floats
  are decimal or exponent. Go additionally refuses `inf` and `nan`
  (**Change**, `certainty = nan` would silently never match);
- booleans are the same eight words, any case. Anything else is an error,
  not the default;
- `%` is literal (**Change**, no interpolation; the shipped config uses
  none, and configparser would crash on a lone `%`);
- a missing key gets the fallback below, and a missing file is an error
  (compare 12, the CLI says which path it tried).

The parser gets a fuzz test that compares it against a reference run of
both real parsers over a corpus. The goal: whenever Go accepts a file,
configparser gives the same values, and INIReader does too for the keys
PAM reads.

### Keys

The fallback is what the code passes to the getter; the effective value is
what's in force when the key is missing. compare.py reads several keys
twice, and the later read wins.

| section | key | type | fallback | effective | read by |
|---|---|---|---|---|---|
| core | use_cnn | bool | false | false | compare, add |
| core | disabled | bool | false | false | PAM; `disable` edits it |
| core | detection_notice, timeout_notice, no_confirmation, suppress_unknown, abort_if_ssh, abort_if_lid_closed, workaround | | | | PAM only (`timeout_notice` and `suppress_unknown` aren't read by anything in the patched tree) |
| video | certainty | float | 3.5 | 3.5, divided by 10 | compare; shown by snapshot |
| video | required_matches | int | 1 | `max(1, value)` | compare |
| video | timeout | int (s) | 4 | 4 | compare |
| video | device_path | string | none | required | all camera users |
| video | warn_no_device | bool | true | true | camera open |
| video | max_height | float | 320.0 | 320.0 | compare |
| video | frame_width, frame_height | int | -1 | -1 = keep the driver's | v4l2 open |
| video | device_fps | int | -1 (v4l2 reader), 0 (OpenCV) | > 0 sets S_PARM | v4l2 open |
| video | dark_threshold | float | 50.0 then 60 | **60** in compare's loop; 60 in add; the first read (50.0) is dead | compare, add |
| video | detection_upsample | int | 1 | 1 | compare, add |
| video | enroll_frames | int | 15 | `max(1, value)` | add |
| video | exposure | int | -1 | -1 = leave auto exposure alone | compare, snapshot (read but unused) |
| video | rotate | int | 0 | 0 | compare (add ignores it) |
| video | recording_plugin | string | opencv | | Python only. Go always uses V4L2 and ignores this, `device_format` and `force_mjpeg` |
| snapshots | save_failed | bool | false | false | compare |
| snapshots | save_successful | bool | false | false | compare |
| rubberstamps | enabled | bool | false | false | compare |
| rubberstamps | stamp_rules | multiline | | | rubberstamps |
| debug | end_report | bool | false | false | compare |
| debug | gtk_stdout | bool | false | | compare's auth popup only; dropped with it |
| debug | verbose_stamps | bool | | | rubberstamps |

## 4. howdy-compare

### 4.1 Order of work in Python

1. Read config (silently, for the early camera start only). If
   `recording_plugin = v4l2` and the model file exists, open the camera now.
   Any error here is swallowed and the camera opens later.
2. Set `OPENBLAS_NUM_THREADS`, `OMP_NUM_THREADS`, `GOTO_NUM_THREADS` to 1
   unless already set, then import dlib, cv2 and numpy.
3. No `argv[1]`: exit 12.
4. Load the model JSON, flatten every model's `data` into one list of
   encodings. Missing file: 10. Empty list: 10.
5. Read config for real.
6. Try to start `howdy-gtk --start-auth-ui` (see 4.9).
7. Start the dlib loader thread, open the camera (or adopt the early one),
   wait for the loader.
8. Work out the scaling, create CLAHE, enter the frame loop.

### 4.2 Order of work in Go

1. `setrlimit`: RLIMIT_CORE 0; RLIMIT_CPU `timeout` + 30 s; RLIMIT_AS the
   current VmSize plus 4 GiB. A fixed cap doesn't work because most of the
   address space is reserved before `main` and never touched: OpenBLAS
   maps 2 GiB unless `OMP_NUM_THREADS=1` is set, and the Go runtime about
   1.7 GB. Measured with the packaged build and test frames up to 611x480:

   | env | detector | VmSize at start | VmPeak |
   |---|---|---|---|
   | PAM's (both BLAS vars) | HOG, upsample 0 and 1 | 1.78-1.85 GB | 2.26-2.40 GB |
   | PAM's | CNN, upsample 0 and 1 | 1.78-1.85 GB | 3.78 GB |
   | `OPENBLAS_NUM_THREADS=1` only | HOG | 3.75-3.82 GB | 4.16-4.24 GB |
   | `OPENBLAS_NUM_THREADS=1` only | CNN | 3.75 GB | 5.62-5.69 GB |

   The old fixed 4 GiB left HOG 12-30 MB to spare without
   `OMP_NUM_THREADS`, and CNN aborted every time. Running out inside dlib
   isn't a clean error: libstdc++ can't allocate the exception and calls
   `std::terminate`, so compare dies on SIGABRT and PAM falls through to
   the next module.
2. Validate `argv[1]` against `^[a-z_][a-z0-9_-]*[$]?$`, at most 32 bytes,
   else 12. **Change** (Python doesn't validate).
3. Read config. Then run two goroutines at once:
   - camera: check `device_path` exists, open, configure, STREAMON;
   - models: `lstat` the model path (it must be a regular file owned by
     uid 0, not a symlink, else 12; missing, 10), read and parse it, then
     load the dlib files on the dlib goroutine, which owns every dlib
     object from then on.
4. Join both. Report the first error in the order of the table in 1 (no
   model beats bad device: Python loads the model before touching the
   camera).
5. If started as root, drop privileges (4.8).
6. Frame loop.

### 4.3 Frame loop

Per iteration, `frames += 1` first, then:

1. If `now - first_loop_start > timeout`: write the failure snapshot if
   `save_failed`, then exit 13 if `dark_tries == valid_frames`, else 11.
   The clock starts at the first loop, not at process start, so the
   effective time budget is setup plus `timeout`.
2. Read the newest frame (4.5). This yields the BGR frame and its grey
   version. For GREY and YUYV cameras the three BGR channels are equal and
   OpenCV's BGR2GRAY gives the grey plane back exactly (its fixed-point
   weights sum to 1), so Go keeps the grey plane and builds BGR only where
   dlib needs it.
3. `gs = CLAHE(grey)`, clip limit 2.0, 8x8 tiles.
4. If snapshots are enabled and fewer than 3 are kept, keep this BGR frame.
   These are the first 3 frames read, dark and black ones included, before
   scaling.
5. Histogram of `gs` in 8 bins over [0, 256). `darkness = bin0 / total * 100`,
   where bin0 counts pixels < 32. calcHist gives float32 and the maths
   stays float32 (the comparisons with 100 and `dark_threshold` too), so Go
   computes `darkness` as float32.
6. If `total == 0` or `darkness == 100`: `black_tries += 1`, next frame.
7. `dark_running_total += darkness`, `valid_frames += 1`.
8. If `darkness > dark_threshold`: `dark_tries += 1`, next frame.
9. If `scaling_factor != 1`, resize BGR and `gs` by it with INTER_AREA (4.6).
10. Rotation, if `rotate` is 1 or 2:
    - 1: frames % 3 == 1 → 90° counter-clockwise, == 2 → 90° clockwise,
      == 0 → as is;
    - 2: even frames → counter-clockwise, odd → clockwise.

    Both images are rotated.
11. Detect faces on `gs` with `detection_upsample`. The HOG detector loads
    from `frontal_face_detector.svm` (falls back to dlib's built-in one if
    that fails). With `use_cnn`, the MMOD detector from
    `mmod_human_face_detector.dat` is used, and its `.rect` is the box.
12. For each face in detector order:
    - landmarks: 5-point `shape_predictor` on the **BGR frame, not the
      CLAHE one**;
    - encoding: `compute_face_descriptor(bgr, landmarks, 1)` (no jitter,
      150 px chip), 128 floats. Also on the un-equalised frame. The
      encoding-parity test has to feed dlib the same un-equalised image;
    - `d = min over all stored encodings of the Euclidean distance`, index
      `i` of the best one;
    - `lowest_certainty = min(lowest_certainty, d)` (it starts at 10);
    - if `0 < d < certainty / 10`: if this frame hasn't been counted yet,
      count it (`matched_frames += 1`). If `matched_frames < required_matches`,
      try the next face in this frame. Otherwise it's a match: print the
      end report if enabled, write the success snapshot if
      `save_successful`, run the rubberstamps (4.10), exit 0.

      `d == 0` is rejected on purpose (a stored vector replayed exactly).
13. If `exposure != -1` (only reached when no face matched in a frame that
    got this far, so not after black or dark frames): set
    `V4L2_CID_EXPOSURE_AUTO = 1` (manual) and
    `V4L2_CID_EXPOSURE_ABSOLUTE = exposure`. Python does this every frame
    because some cameras ignore it otherwise; Go does the same.

Distance maths is float64. The flattened encoding list and the per-face
encoding are compared exactly as numpy does (`norm(enc - face, axis=1)`).
Sub-ulp differences don't matter for the `<` test except at the edge, which
is accepted.

### 4.4 Scaling

`height` is the frame height (the width when `rotate == 2`), or 1 if 0.
`scaling_factor = (max_height / height) or 1`, so a `max_height` of 0 means
no scaling. **Python resizes whenever the factor isn't 1, which scales up
too**: a 640x480 camera with the default `max_height` 320 is shrunk, and a
camera smaller than `max_height` is enlarged. Go only shrinks. A camera
whose height equals `max_height` is never resized. See [Deviations](#deviations).

### 4.5 Camera (port of `recorders/v4l2_reader.py`)

- `open(device_path, O_RDWR | O_NONBLOCK)` (Go adds `O_CLOEXEC`).
- `VIDIOC_ENUM_FMT` until it fails; pick GREY, else YUYV, else error (14).
- `VIDIOC_G_FMT`, set the pixel format, plus width/height if > 0,
  `VIDIOC_S_FMT`. Use whatever the driver returns. Error (14) if the
  pixel format changed.
- If `device_fps > 0`: `VIDIOC_S_PARM` with timeperframe 1/fps, errors
  ignored.
- `VIDIOC_REQBUFS` 4 mmap buffers, `QUERYBUF` and mmap each, `QBUF`,
  `STREAMON`.
- Read: `select` up to 2 s (timeout: 14). Dequeue until EAGAIN and keep only
  the newest buffer, re-queueing the rest. Copy out `bytesused`, then
  re-queue it. Skip it if `V4L2_BUF_FLAG_ERROR` (0x40) is set or it's
  shorter than `bytesperline * height`. Rows are `bytesperline` apart. YUYV
  luma is every other byte.
- ioctl request numbers are masked to 32 bits.
- Release: STREAMOFF, munmap, close.

### 4.6 Image operations (Go, matched to OpenCV)

- **CLAHE**, as `cv::createCLAHE(2.0, (8, 8))` for 8-bit images: if the size
  isn't a multiple of 8 in either direction, pad with BORDER_REFLECT_101 to
  the next multiple for the histograms. Per tile: 256-bin histogram,
  `clip = max(1, int(2.0 * tile_area / 256))`, clip it, spread the excess
  evenly (`excess / 256` to every bin, then the remainder one bin at a time
  with step `max(256 / remainder, 1)`), cumulative sum, LUT =
  `saturate(round(cdf * 255 / tile_area))`. Each output pixel is the
  bilinear blend of the four neighbouring tile LUTs at tile-centre
  coordinates, clamped at the edges, and rounded. Accepted at ±1 grey level
  against `cv2.createCLAHE` on test images (640x360 gives 80x45 tiles, no
  padding).
- **Histogram**: 8 equal bins over [0, 256).
- **INTER_AREA**: only needed if `scaling_factor != 1`. For integer
  downscales it's a box average. For fractional ones, OpenCV weights each
  source pixel by its overlap. When enlarging, OpenCV's INTER_AREA acts like
  bilinear. Go implements the downscale cases and tests them against cv2.
- **Grey → BGR**: copy the plane three times.
- **Rotation**: 90° either way.

### 4.7 Snapshots (`snapshot.py`)

- Frames: up to 3 BGR frames (compare), 4 (`howdy snapshot`). Nothing is
  written if there are none.
- The frames go side by side, then a bottom border
  `len(lines) * 20 + 40` px high in (44, 44, 44).
- If there's more than one frame, `logo.png` (180x57) is pasted at
  `y = frame_height + 20`, `x = frame_width * n - 210`.
- Each text line goes at `x = 30`, `y = frame_height + 30 + i * 20`, in
  Hershey Simplex at scale 0.4, white, anti-aliased. Go uses
  `golang.org/x/image/font/basicfont`, so the pixels differ while the
  layout stays.
- The dir is created with mode 0750 if missing.
- File name: UTC `%Y%m%dT%H%M%S.jpg`. Opened with
  `O_WRONLY|O_CREAT|O_EXCL|O_NOFOLLOW`, mode 0640. If the open fails (exists,
  symlink, EACCES), the snapshot is silently skipped. A second snapshot in
  the same second is lost. JPEG quality is 95 (OpenCV's default).
- compare's lines:
  - `FAILED LOGIN` / `SUCCESSFUL LOGIN`
  - `Date: 2026/01/02 03:04:05 UTC`
  - `Scan time: ` + `round(seconds since the first loop, 2)` + `s`, as Python prints floats (`0.53s`, `8.0s`)
  - `Frames: ` + frames + ` (` + `round(fps, 2)` + `FPS)`
  - `Hostname: ` + nodename
  - `Best certainty value: ` + `round(lowest_certainty * 10, 1)`: `3.1`, but
    `100` with no `.0` if no face was seen, because it starts as the int 10
- `howdy snapshot`'s lines: `GENERATED SNAPSHOT`, the date line,
  `Dark threshold config: ` + str(float) (`60.0`),
  `Certainty config: ` + str(float) (`3.5`).

Go needs a small formatting package for the three forms seen here:
Python float `str()`/`repr` (`8.0`, `0.53`, `1e-05`), Python int-or-float
from `round()` (`100` vs `3.1`), and numpy float32 scalar `str()`
(`55.113888`, `60.0`). Each is checked against python3 on a table of
values.

### 4.8 Privileges (new)

hyprlock runs compare as the user: there's nothing to drop, and snapshots go
to the group-writable dir.

`PR_SET_NO_NEW_PRIVS` is per thread, and Go's `AllThreadsSyscall` refuses
to run in a cgo binary, so a `prctl` from Go would only cover one thread.
It's set instead in a cgo `__attribute__((constructor))`, which runs before
the Go runtime starts any thread, so every thread inherits it. It only
affects execve, so it doesn't get in the way of the uid drop below. Every
run sets it, as root or not.

Under sudo, compare starts as root. After the camera fd is open, the model
has been read and the dlib files are loaded:
`setgroups([])`, `setresgid(nobody)`, `setresuid(nobody)` (looked up from
`/etc/passwd` without NSS, usually 65534). In a cgo binary, Go's `syscall` versions
of these call glibc's wrappers, which apply the change to every thread
(`syscall_linux.go` in Go 1.27: `cgo_libc_setresuid` when cgo is linked,
`AllThreadsSyscall` otherwise). Then check, else exit 12:
- `getresuid`/`getresgid` show nobody in all three slots;
- `getgroups` is empty;
- `/proc/self/task/*/status` shows `Uid`, `Gid` and `NoNewPrivs: 1` the
  same on every thread.

The dlib goroutine is idle at that point.

**Snapshots when started as root: skipped.** The open has to happen after
the drop, because the first frame decides whether there's a snapshot and
what it shows. But the drop is what takes away write access to the
snapshots dir. A dir fd opened beforehand doesn't help: `openat` checks
permissions with the creds at call time. The other options are dropping to
uid nobody with the snapshots dir's group (makes nobody a member of the
user's group), or a root parent that writes the JPEG for the child (more
code running as root). Lock screen attempts, the case snapshots are mostly
for, still get them. If `save_failed` or `save_successful` is on, compare
prints once to stderr that snapshots are off under root.

### 4.9 howdy-gtk auth popup

compare.py starts `howdy-gtk --start-auth-ui` and writes `M=<text> \n` and
`S=<text> \n` lines to its stdin ("Starting up...", "Identifying you...",
"Scanned N frames (skipped M dark frames)"). Under PAM the environment is
empty, so GTK can't find a display: running it with `env -i` fails with
"Gtk couldn't be initialized". The popup never appears from a PAM unlock
today. **Change:** Go doesn't start it. That also keeps compare free of
subprocesses.

### 4.10 Rubberstamps

With `[rubberstamps] enabled = true`, compare.py imports the rubberstamps
module after a match and runs `stamp_rules` (nod, hotkey). A failing
`faildeadly` stamp exits 15. They aren't ported yet. **Change:** if
`enabled` is true, compare exits 12 before opening the camera (fail
closed). Accepting without them would silently weaken a setup that asked
for more checks.

### 4.11 End report (`[debug] end_report`)

Printed to stdout on a match, before the snapshot. Exact text, with
`%d` meaning rounded milliseconds:

    Time spent
      Starting up: %dms
      Open cam + load libs: %dms
        Opening the camera: %dms
        Importing recognition libs: %dms
      Searching for known face: %dms
      Total time: %dms

    Resolution
      Native: %dx%d
      Used: %dx%d

    Frames searched: %d (%.2f fps)
    Black frames ignored: %d 
    Dark frames ignored: %d 
    Certainty of winning frame: %.3f
    Frames that matched: %d            (only if required_matches > 1)
    Winning model: %d ("%s")

Details:
- "Black/Dark frames ignored" end with a space.
- `Native` prints height x width and `Used` prints height x width of the
  scaled frame. The labels say otherwise. Kept as is.
- "Starting up" is process start to just before the camera. In Go it's
  process start to config read.
- "Open cam + load libs" is the max of the two. In Go both overlap for real.
- **Winning model is a bug.** `match_index` is an index into the flattened
  encodings, but it's used to index `models`. It's right only while every
  model has exactly one encoding (true for everything `howdy add` writes),
  and it crashes or names the wrong model otherwise. **Change:** Go maps the
  index back to the model it came from, and prints that model's position.

The two dark-timeout messages (exit 13):

    All frames were too dark, please check dark_threshold in config
    Average darkness: {avg}, Threshold: {threshold}

`{avg}` is the `str()` of a numpy float32 scalar, e.g. `55.113888` or
`60.0`: with OpenCV 5, `calcHist` returns shape (8,), so `hist[0]`,
`darkness` and the running total are float32 scalars. The total is summed
in float32, so Go sums in float32 too. If every frame was black, the total
is still the int 0, and `0 / 1` prints `0.0`. `{threshold}` is
`str(float)`, e.g. `60.0`. (OpenCV 4 returned shape (8, 1), which printed
as a one-element array, `[55.113888]`. Go follows the OpenCV 5 form, which
is what Arch ships.)

Other compare messages:
- missing dlib data:
  `Data files have not been downloaded, please run the following commands:`,
  blank, `\tcd /usr/share/dlib-data`, `\tsudo ./install.sh`, blank;
- no device: `Howdy could not find a camera device at the path specified in the config file.`,
  `It is very likely that the path is not configured correctly, please edit the 'device_path' config value by running:`,
  blank, `\tsudo howdy config`, blank;
- read failure: `Failed to read camera specified in the 'device_path' config option, aborting`.

Go never logs encodings, frames or distances to syslog. It doesn't syslog
at all: PAM already logs every outcome.

## 5. Face model file

`/etc/howdy/models/<user>.dat`:

    [{"time": 1730000000, "label": "Model #0", "id": 0, "data": [[0.1, -0.0123, ...128 floats]]}, ...]

- Written by `json.dump(list, file)` with default options: separators
  `", "` and `": "`, key order `time, label, id, data`, no trailing newline,
  `ensure_ascii` (non-ASCII as `\uXXXX`, astral characters as surrogate
  pairs, `"` and `\` escaped, control characters as `\n` `\t`... or
  `\u00XX`).
- Floats print as Python's `repr`: shortest round-trip, always with `.0` or
  an exponent (`1.0`, `-0.0`), exponent form when the decimal exponent is
  below -4 or at least 16 (`1e-05`, `2.5e-07`, `1.2345678901234568e+17`).
  Go's `encoding/json` and `strconv 'g'` differ (`1`, `1.234567e+06`), so
  model writing uses its own encoder. Tested by dumping synthetic encodings
  with python3 and Go and diffing bytes. No real face data in tests.
- Encodings from dlib are float32 widened to double
  (`0.10000000149011612`). Averaged ones are arbitrary float64.
- `time` is `int(time.time())`, `id` an int. `howdy add` sets
  `next_id = last_entry.id + 1` (0 if empty), so ids can have gaps.
- Reading: Go accepts what Python's `json.load` accepts for these files
  (any whitespace, ints or floats in `data`). It refuses `NaN` and
  `Infinity` (Python would accept them), missing keys, non-list `data`, and
  encodings that aren't 128 long. howdy-gtk never opens the file (it lists
  the models dir and calls `howdy list --plain`).
- `remove` (and `add`, for the entries already there) writes back what it
  loaded: `json.dump(json.load(f))`. That keeps each entry's key order as it
  was in the file and any unknown keys, keeps `0` an int (not `0.0`), and
  drops the original whitespace. So Go holds models as ordered JSON values
  that keep int vs float, not as a struct. Only new entries get the
  `time, label, id, data` order. `internal/model` tests this with a synthetic file
  that has an extra key, reordered keys and an int in `data`, run through
  Python and Go and compared byte for byte.
- Writing (the patched `user_models.save`): unlink `<path>.tmp` if it
  exists, then `open(<path>.tmp, O_WRONLY|O_CREAT|O_EXCL|O_NOFOLLOW, 0640)`,
  `fchown(0, <user's primary gid>)` when euid is 0, write, `fsync`,
  `rename` over the model. On any error the tmp file is removed. Go also
  `fsync`s the directory after the rename.

## 6. The howdy CLI

### 6.1 Arguments

argparse, `prog="howdy"`:

    howdy [-U USER] [--plain] [-h] [-y] {command} [{arguments}...]

- Options may appear before the command, between it and its arguments,
  or after them (`add -y lbl`, `add lbl -y -U x`). They can't split the
  arguments: `add a -y b` is an error (exit 2), since argparse fills
  `arguments` from one run of positionals. howdy-gtk relies on that: it runs
  `["howdy", "add", <label>, "-y", "-U", <user>]`,
  `howdy list --plain -U <user>` (through a shell), `howdy add -y`,
  `howdy set device_path <path>` and `howdy set certainty <n>`. Setup
  scripts often edit config.ini with `sed 's/^certainty = .*/.../'`, which
  writes plain `key = value` lines the strict parser accepts. Go's `flag`
  stops at the first non-flag, so the CLI uses a small parser of its own
  that accepts flags anywhere and `--` to end them.
- `command` must be one of add, clear, config, disable, list, remove, set,
  snapshot, test, version. Otherwise argparse prints the usage and
  `howdy: error: argument command: invalid choice: 'x' (choose from ...)`
  to stderr and exits 2. Go matches the exit code and the gist of the
  message.
- `howdy` alone prints `current active user: <user>`, a blank line, then the
  help, and exits 0, without root.
- `-h` prints the help, without root.

Default user: the first non-empty of `SUDO_USER`, `DOAS_USER`, the name for
`PKEXEC_UID`, and `getpass.getuser()` (LOGNAME, USER, LNAME, USERNAME, then
the passwd entry for the uid). If none: `Could not determine user, please use the --user flag`,
exit 1.

Then, in this order:
- not euid 0: `Please run this command as root:`, blank, `\tsudo howdy <args>`,
  exit 1;
- user is `root`: `Can't run howdy commands as root, please run this command with the --user flag`,
  exit 1;
- **Change:** user fails the username pattern: Go prints an error and exits
  1. Python builds `/etc/howdy/models/<user>.dat` from it as is, so
  `-U ../x` escapes the models dir, as root.

### 6.2 Commands

Messages are exact. `{x}` is a substitution. Every exit not listed is 0.

**add [label]**
1. dlib data missing: the same hint as compare, exit 1.
2. Models dir missing: `No face model folder found, creating one`, then
   create it (Python's `makedirs` gives 0755 under umask 022; Go creates
   it 0755 root:root explicitly).
3. Load the existing models (missing file means `[]`).
4. More than 3 already: `NOTICE: Each additional model slows down the face recognition engine slightly`,
   `Press Ctrl+C to cancel`, blank.
5. Without `--plain`: `Adding face model for the user <user>`.
6. Label: `arguments[0]` if given, else `Model #<next_id>`. With `-y`:
   `Using default label "<label>" because of -y flag`. That message prints
   whatever label was picked, the argument included. Otherwise prompt
   `Enter a label for this new model [<label>]: `, and a non-empty answer
   replaces it, cut to 24 code points. The argument isn't cut.
7. If the label contains `,`: `NOTICE: Removing illegal character "," from model name`,
   and every comma is removed (`list --plain` is comma-separated).
8. Open the camera (same errors as compare; `device_path` missing → 14 with
   the hint).
9. Blank line, `Please look straight into the camera`, sleep 2 s, then read
   and discard frames for 0.5 s.
10. Up to `60 + enroll_frames * 6` frames, until `enroll_frames` encodings:
    grey, CLAHE, histogram, skip black (not counted), count valid frames,
    skip if darkness > `dark_threshold`, then detect with
    `detection_upsample`. More than one face: release the camera,
    `Multiple faces detected, aborting`, exit 1. One face: landmarks and
    encoding from the BGR frame. `add` ignores `rotate` and `max_height`.
11. No encodings:
    - no valid frames: `Camera saw only black frames - is IR emitter working?`;
    - all valid frames dark: the two dark messages, `{avg}` printed as
      in 4.11;
    - else: `No face detected, aborting`.

    Exit 1.
12. Fewer than `min(3, enroll_frames)`: `Only saw a face in {count} frames, please hold still and try again`,
    exit 1.
13. `dist_i = |enc_i - mean(enc)| * 10`. Keep
    `dist_i <= max(median(dist) * 1.5, sorted(dist)[(n - 1) // 2])`, and
    average the kept ones. Row sums run in order, then get divided by the
    count, as numpy does for axis 0 on a C-ordered array, so Go gets the
    same bits. `spread_i = |kept_i - avg| * 10`.
14. Without `--plain`: blank, then
    `Used {used} of {total} frames, spread {median:.2f} (worst {worst:.2f})`.
15. Append `{time, label, id, data: [avg]}`, save as in 5. Then a blank line,
    `Scan complete`, `Added a new model to <user>`.

The HOG detector loads from the `.svm` file here too. It's the same detector
as `get_frontal_face_detector()`, just faster to load.

**remove <id>**
- no argument: `Please add the ID of the model you want to remove as an argument`,
  `For example:`, blank, `\thowdy remove 0`, blank,
  `You can find the IDs by running:`, blank, `\thowdy list`, blank, exit 1.
- models dir missing: `Face models have not been initialized yet, please run:`,
  blank, `\thowdy add`, blank, exit 1.
- no model file: `No face model known for the user {user}, please run:`,
  blank, `\thowdy add`, blank, exit 1.
- The id is matched as a string against `str(id)`. Without `-y`:
  `This will remove the model called "{label}" for {user}`, prompt
  `Do you want to continue [y/N]: `. Anything other than y/Y prints a blank,
  `Interpreting as a "NO", aborting`, exit 1. Otherwise a blank line.
- not found: `No model with ID {id} exists for {user}`, exit 1.
- last model: delete the file, `Removed last model, howdy disabled for user`.
- else save the rest, `Removed model {id}`.

**list**
- models dir missing: `Face models have not been initialized yet, please run:`,
  blank, `\tsudo howdy -U <user> add`, blank, exit 1.
- no model file: exit 1, and unless `--plain`,
  `No face model known for the user {user}, please run:`, blank,
  `\tsudo howdy -U <user> add`, blank.
- unless `--plain`: `Known face models for {user}:`, blank, then
  `\033[1;29mID  Date                 Label\033[0m`.
- per model: `--plain` gives `{id},{YYYY-MM-DD HH:MM:SS},{label}`; otherwise
  the id padded with spaces to 4 columns (none if longer), the date, two
  spaces, the label. Dates are in local time (TZ from the environment, then
  `/etc/localtime`).
- then one empty line, both modes.

howdy-gtk splits each line on `,` and drops lines with fewer than 3 fields.

**clear**
- models dir missing: `No models created yet, can't clear them if they don't exist`, exit 1.
- no model file: `{user} has no models or they have been cleared already`, exit 1.
- without `-y`: `This will clear all models for <user>`, the same y/N
  prompt and abort message.
- delete the file, blank, `Models cleared`.

**config**
- Editor: `$EDITOR` if it resolves on PATH as a single word, else `nano`,
  else `vi`. `Opening config.ini in {basename}`, then run
  `[editor, /etc/howdy/config.ini]` as argv, no shell, and wait. Spawn
  failure: `Failed to open editor: {error}`.
- None found: `Error: Could not find a suitable text editor.`,
  `Please install 'nano' or 'vi', or set the EDITOR environment variable.`,
  `If you are running this command with sudo, try 'sudo -E howdy config' to preserve your EDITOR variable.`
  Exit 0 (Python doesn't set one). Go keeps 0.

**disable <0|1|true|false>**
- no argument: `Please add a 0 (enable) or a 1 (disable) as an argument`, exit 1.
- else `1`/`true` (any case) means `true`, `0`/`false` means `false`,
  otherwise `Please only use 0 (enable) or 1 (disable) as an argument`, exit 1.
- already that value: `The disable option has already been set to {value}`,
  exit 1. If the key is missing, Python compares against the bool `True`,
  which never equals the string, then replaces `disabled = True`, a no-op,
  and still reports success.
- Python rewrites the file in place, replacing the substring
  `disabled = <old>` on every line. **Change:** Go sets only `[core]
  disabled`, keeps every other byte, writes a tmp file and renames it
  (same mode and owner), and adds the key under `[core]` if it's missing.
- `Howdy has been disabled` / `Howdy has been enabled`.

**set <name> <value>**
- fewer than 2 arguments: `Please add a setting you would like to change and the value to set it to`,
  `For example:`, blank, `\thowdy set certainty 3`, blank, exit 1.
- Python takes the last line in the file that starts with `<name> `, in any
  section, then replaces that whole line wherever it appears with
  `<name> = <value>`. Not found: `Could not find a "{name}" config option to set`,
  exit 1.
- **Change:** Go matches `^<name>\s*[=:]` (a key, not a prefix), refuses a
  name present in more than one section (asks for `section.name`), refuses
  values with a newline or that the parser would refuse, and writes
  atomically as for `disable`.
- `Config option updated`.

**snapshot**
- Open the camera, read one frame (it wakes the emitter), read 4 more,
  write the snapshot (4.7) with the `howdy snapshot` lines.
- Saved: `Generated snapshot saved as`, then the path. Not saved:
  `Could not save the snapshot`. Exit 0 either way.
- Run as root by the CLI, so the file is root-owned 0640 in the snapshots
  dir.

**version**
- `Howdy 3.0.0 BETA` (after the root check, as in Python). Go keeps that
  exact first line so scripts that grep it keep working, and adds a second
  line with its own version.

**test**
- **Change:** a terminal readout instead of test.py's OpenCV window, which
  can't open under sudo on Wayland. One line per frame (darkness, faces,
  recognition time, certainty and label or `no match`), and on ctrl+C the
  fps, how many frames were dark or had a face, and the best and median
  certainty with how many faces were under `certainty`.
- Same as test.py: a missing model only means no matching, dark frames
  are skipped by `dark_threshold`, exposure is set on every frame. Unlike
  it, frames are shrunk to `max_height` as compare does, so the numbers
  are the ones an unlock sees.
- Only `test` catches SIGINT, so ctrl+C still kills the other commands.

## 7. Security

Kept from the patched Howdy:
- snapshot opens with `O_EXCL|O_NOFOLLOW`, 0640, dir 0750 (4.7);
- model writes: tmp file, `O_EXCL|O_NOFOLLOW`, 0640 root:<gid>, fsync,
  rename (5);
- enrollment: settle, discard 0.5 s, average with outlier drop, abort on
  two faces (6.2 add);
- `required_matches` counts each frame once (4.3).

New in Go:
- username validated before any path is built (compare and CLI);
- model refused if it's a symlink, not a regular file, or not owned by
  root;
- root drop under sudo (4.8);
- RLIMIT_CORE 0, RLIMIT_CPU, RLIMIT_AS;
- fixed envp from PAM, `O_CLOEXEC` on every fd;
- no subprocesses in compare; the CLI runs only the editor, as argv;
- BLAS pinned to 1 thread twice: PAM's envp, and
  `openblas_set_num_threads(1)` via `dlsym(RTLD_DEFAULT)` from the cgo
  init, before any dlib call;
- nothing logs encodings, frames or distances;
- built with `-buildmode=pie -trimpath -mod=readonly -modcacherw`,
  makepkg's `CGO_*FLAGS`, and checked for PIE and full RELRO.

## Deviations

Everything marked **Change** above. Each one lives in one place in the code.

1. **Exit 12 instead of a crash (exit 1)** for unreadable or malformed
   models, unparseable config, missing `device_path` and missing dlib data
   (1). Models whose `data` lists are all empty give 10.
2. **Strict config parsing** (3): refuse files with duplicates or
   case-only section clashes; refuse values with ` ;` (PAM reads those
   differently); no `nan`/`inf`; no `%` interpolation; invalid booleans are
   errors.
3. **Only shrinks.** Python resizes whenever `max_height` differs from the
   frame height, enlarging small frames too. Go only ever shrinks, so a
   camera smaller than `max_height` is used at its own size.
4. **No auth popup.** It already can't start under PAM (4.9).
5. **Rubberstamps enabled means exit 12** until they're ported (4.10).
6. **Winning model** in the end report maps back to the right model (4.11).
7. **Snapshots skipped under sudo**, after the root drop (4.8).
8. **CLI hardening**: username validation, `set` matching whole keys and
   refusing newlines, `disable` editing only `[core] disabled`, all config
   writes atomic (6).
9. **recording_plugin ignored**: always V4L2, and cameras with neither GREY
   nor YUYV (MJPEG-only) get 14 where Python would fall back to OpenCV.
10. **howdy test is a terminal readout**, not a window (6.2 test).

Still open: rubberstamps (4.10).

## Packaging

`howdy-go-git` replaces the Python howdy package, which also ships the PAM
module and howdy-gtk. `packaging/arch` builds those from a local Howdy
checkout; `packaging/aur` builds upstream Howdy at a pinned commit with
`pam-compare-path.patch`. Both build, and the two packages hold the same
files. Only the shipped `config.ini` differs: upstream's has no
`required_matches` or `enroll_frames`, so those fall back to 1 and 15.

The PAM patch adds a `compare_path` meson option next to `python_path`
(python still builds howdy-gtk's wrapper; empty means the old behaviour),
execs `howdy-compare` with `OPENBLAS_NUM_THREADS=1 OMP_NUM_THREADS=1`
instead of an empty environment, and fixes the "Unknown error" pointer
arithmetic (1). Against upstream it also carries the socket
redirect for polkit from boltgolt/howdy#1146 and the C++20 `optional_task` fix.

## Measured

On the laptop this was written on (an IR camera at 15 fps, HOG,
`detection_upsample = 0`, packaged build, through PAM):

| | Python Howdy | howdy-go |
|---|---|---|
| `sudo true` with a match | 792 ms | 461 ms |
| camera open and model load | | overlapped, about 145 ms |
| startup to exit 10 (no model) | | 9 ms |

- Live frames: on 58 IR frames with a face, Go's distance equals
  python-dlib's to three decimals.
- Every other frame is dark from the strobing emitter. Those cost 12 ms
  each and don't change when the next lit frame arrives.
- Enrollment decides the frame count. With an old model of two
  single-frame encodings the genuine distances had a median of 4.36 and
  an unlock took 8 to 24 frames; after enrolling again with `howdy add` it
  matched on the second frame at 2.8.
