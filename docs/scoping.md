# Scoping: rubberstamps, other encoders, MJPEG

What each of these would take, what has to happen first, and what I'd do.
None of it is started. The numbers come from `tools/face-eval.py`, run on the
recordings described in [sface.md](sface.md): one user, two other people, and
one phone attack.

## Rubberstamps

**What exists.** Upstream runs them in `compare.py` after a face match.
`rubberstamps.execute` reads `[rubberstamps] stamp_rules`, which has one rule
per line (`type timeout (failsafe|faildeadly) key=value ...`). For each rule
it loads `rubberstamps/<type>.py` and runs it. If a stamp returns false, the
process exits with 15. If a stamp times out, the result is `not failsafe`.
Upstream ships two stamps, `nod` and `hotkey`. howdy-go refuses to run with
`enabled = true` (exit 12). That's a safe state.

**What a port would be.** A new `internal/rubberstamp` package, called from
`compare.Loop` once a match succeeds. It returns 0, or `compare.Rubberstamp`
(15, which the PAM module already defines and understands).

| piece | work | notes |
|---|---|---|
| rule parser | small | upstream's regex, plus the same errors for unknown types and options. The ini parser already handles multi-line values. |
| `nod` | about a day with tests | Tracks the nose point across frames, relative to eye distance, and counts direction changes on each axis. Needs exactly one face per frame, with landmarks. |
| `hotkey` | not recommended, see below | |
| third-party stamps | refuse | Upstream loads any `.py` file in the directory into a process started as root. A Go build can't do that, and shouldn't. Unknown types stay an error, never a silent skip. |

**`nod` needs a landmark abstraction.** Upstream reads dlib's 5-point
predictor. Eyes are points 0 and 2, and the nose is point 4. `face.Rect` only
has `Landmarks` for YuNet (eyes first, nose third). For dlib, the points come
out of `Encode` as `Encoding.Parts`. A small `face.Pose` that both backends
fill, with the eye distance and the nose point, would keep the stamp
independent of the recogniser. The tracking maths should be a pure function
of a landmark sequence. That makes it testable with made-up trajectories (a
nod, a shake, a still face, a face that leaves the frame), with no camera.

**Why not `hotkey`.** It reads the keyboard through `/dev/input`, which needs
root or the `input` group. howdy-compare drops root right after the camera
opens. So it would have to open every keyboard device first, and hold them
while the stamp runs. Lock screens run compare as the user, who can't read
those devices at all. So it would work for `sudo` but not for the screen
locker, the same as upstream. A process that holds every keyboard open as
root is a bigger attack surface than anything else in this program.

**Questions that need your call.**

1. Do you use rubberstamps? If not, keeping the refusal is the right answer.
2. Upstream `nod` tells the user what to do through howdy-gtk. howdy-go has no
   popup, so with `nod` alone nobody would be told to nod. Whether the prompt
   gets printed to stdout depends on whether the PAM module shows it. That
   needs checking in `pam/main.cc` before we promise anything.
3. `RLIMIT_CPU` is `timeout + 30` seconds, and it has to grow by each stamp's
   timeout.

**What it adds to security.** A still photo can't nod. A video on a phone
can. So it stops the weakest attack and nothing more. It works alongside a
liveness check, but it doesn't replace one.

**Done when:** rule parsing and `nod` have unit tests. The re-exec harness in
`cmd/howdy-compare/main_test.go` covers exit 15 for a failed stamp, 0 for a
passed one, and 12 for an unknown type or option. `enabled = true` with only
unsupported types still fails closed.

## Other encoders (EdgeFace and friends)

We measured these with `yunet` detecting faces, on the same frames. d' is how
far apart the user is from other people. Higher is better.

| encoder | d' | encode ms | size | licence |
|---|---|---|---|---|
| sface (shipped) | 7.23 | 12.5 | 39 MB | Apache-2.0 |
| edgeface_base | 6.91 | 19.1 | 73 MB | BSD-3 |
| edgeface_xxs | 5.71 (EER 0.03) | 2.4 | 5 MB | BSD-3 |
| edgeface_xs, s | 4.6, 5.0 | 3.6, 6.2 | 7, 15 MB | BSD-3 |
| adaface_ir_18 | 7.15 | 49 | 96 MB | MIT, data caveat |
| adaface_ir_50 | 8.50 | 105 | 174 MB | MIT, data caveat |
| arcface_mbf, r50 | 7.25, 7.71 | 11, 105 | 14, 174 MB | non-commercial |

**Conclusion: nothing here justifies replacing SFace.** EdgeFace-base is
slower, larger, and no better. The only encoder clearly ahead on d' is
AdaFace-IR50. It takes about eight times SFace's encode time, is four times
the size, and has a training-data caveat. With two other people and one
attacker, a gap of 7.2 against 6.9 is noise. So the table can rule models
out, but it can't pick a better one.

**When it would be worth doing:** SFace fails for someone (a colour camera, a
different face population, glasses), and a larger evaluation shows another
model fixes it. That evaluation needs many more impostors and attack sessions
than one laptop's worth.

**Cost of adding one, for the day it comes:**

1. Constants for the file, sha256, size, and a new encoder tag in
   `internal/backend`. The tag keeps old descriptors from ever being compared
   with new ones.
2. A model table in `internal/sface` (or a sibling package) for input size,
   normalisation, and output name. The ArcFace-aligned 112x112 crop is shared
   by EdgeFace, ArcFace, and AdaFace.
3. A new `recognizer` value in `config`, `loader.Load`, and `compare.Threshold`,
   with its own threshold key. Thresholds don't carry over between models.
4. PKGBUILD source and sha256 for both PKGBUILDs, and the licence file.
5. `docs/sface.md` and the README options table.

**Gate:** it has to beat SFace's margin on held-out sessions, including
cross-glasses frames and the attacks. A better d' alone isn't enough.

## MJPEG cameras

**Is it needed?** Not on the laptop this was built on. Its colour camera
(`/dev/video0`) offers `MJPG` and `YUYV`, and `YUYV` at 640x480 and 640x360 is
already enough for a face. MJPEG would only add higher resolutions and frame
rates, which authentication doesn't use. It matters for cameras that offer
`MJPG` only.

**Design.**

- Pick `GREY`, then `YUYV`, then `MJPG`, so nothing that works today changes.
- Decode with the standard library's `image/jpeg`. It's pure Go, memory safe,
  needs no new dependency, and it runs in a process that parses camera data as
  root.
- Keep only the luma plane. `jpeg.Decode` returns `*image.YCbCr` (or
  `*image.Gray`), and `Y` is already the grey frame, with no colour conversion.
- The chroma planes make the colour-camera warning exact for MJPEG, without
  the YUYV guess.
- A frame that fails to decode counts as a skipped frame, the same as `Read`
  already does for bad buffers.

**Known traps.**

- Many UVC cameras send MJPEG without Huffman tables (the "AVI1" shortcut),
  and Go's decoder rejects those. The standard tables, about 420 bytes, have
  to be inserted when the frame has none.
- Cap the decoded size before allocating, since the camera picks the size.
  `v4l2` already caps at 8192 per side. The decoder should refuse any frame
  whose header disagrees with the format the driver reported.
- Decoding 1080p costs milliseconds, so ask the driver for the smallest size
  that gives `max_height` (the loop shrinks to 320 by default).

**Tests.** JPEG frames made with Go's encoder, the same frames with their
Huffman tables stripped, truncated frames, and a frame whose header lies about
its size. All of them run through the existing `fakeDevice`.

**Gate:** the RGB calibration below has to come first. Adding more colour
webcams to a pipeline whose thresholds were set on IR makes the spoofing
exposure bigger, not smaller.

**RGB calibration doesn't have to wait for MJPEG.** This machine's colour
camera already works over `YUYV`. Recording yourself, other people, and a
photo with `face-capture -device /dev/video0`, then running `face-eval.py` on
them, gives the numbers the thresholds need.
