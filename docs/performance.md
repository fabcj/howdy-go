# Performance

A `sudo true` with a face match takes about 460 ms on a 15 fps IR camera.
This page shows where that time goes and what could be changed. It only
includes things we measured. To run the benchmarks yourself:

    go test -run XXX -bench . ./internal/imgproc ./internal/sface
    HOWDY_GO_TESTDATA=~/.cache/howdy-go/testdata go test -run XXX -bench . ./internal/dlib

Absolute times change with the CPU's power state. On one laptop set to
powersave, the same benchmark ran 2.5 to 3 times slower for a while, then
went back to normal with no change to the code. So compare numbers taken
close together, and repeat a run before you trust a difference.

## Per frame, on one core

| step | fast | slow | notes |
|---|---|---|---|
| CLAHE 640x360 | 0.6 ms | 1.3 ms | 5.2 ms at 1280x720 in the slow state |
| darkness check | 0.06 ms | 0.16 ms | |
| shrink 1280x720 to 360 | 1.1 ms | 2.3 ms | webcams only; IR frames are already small |
| sface detect (YuNet) | 4.3 ms | 9.7 ms | one ONNX Runtime thread |
| sface encode | 17.5 ms | 43 ms | |
| dlib HOG detect, upsample 0 | 8.8 ms | 22 ms | |
| dlib encode | 10 ms | 25 ms | |

A frame the loop skips as dark only costs the CLAHE and the darkness check,
about 1 to 2 ms. A lit frame with a face costs 15 to 55 ms. A strobing IR
camera gives a lit frame every 133 ms, so the loop keeps up and nothing
queues.

## Where the 460 ms goes

The models load while the camera opens, so only the longer of the two
counts. Camera open takes about 145 ms, and the models load in about 55 ms.
After that, the loop waits for a lit frame. That can take up to one strobe
period, which is 133 ms on a 15 fps camera. Then it spends 20 to 55 ms on
the frame. The rest is `sudo` and PAM.

Most of the time goes to the camera and the emitter's strobe. No software
choice here changes that. `end_report = true` prints the same split for a
real unlock.

## Options looked at

- **More ONNX Runtime threads.** In the slow state, encoding went from 43 ms
  with one thread, to 28 ms with two, and 18 ms with four. Detection went
  from 10.7 ms to 7 ms and 5.3 ms. That saves 15 to 25 ms out of about 460.
  The cost is using more cores while you work, and giving up the one-thread
  design that keeps compare out of the way. Not worth a setting.
- **SIMD flags for dlib's HOG code.** The detector is header-only, so
  howdy-go's cgo flags compile it without `-mavx`. With `-msse4.1`, detection
  took 7.8 ms instead of 8.9. With `-mavx` it took 7.3 ms, and with
  `-mavx2 -mfma` it took 6.4 ms. That's a gain of 1 to 2.5 ms. It would also
  stop the binary from running on CPUs without those instructions, and it
  risks the bit-for-bit match with python-dlib. Not worth it.
- **A smaller encoder.** EdgeFace-XXS encodes in 2.4 ms, but it's clearly
  worse at telling people apart (d' 5.7 against 7.2). See
  [scoping.md](scoping.md).
- **int8 SFace.** OpenCV Zoo publishes a quantised SFace. We haven't tested
  it. It would change the descriptors, so it needs the same evaluation as any
  other model before anyone relies on it.
- **Frames with several faces** get encoded once per face. With three faces,
  that's three encodes in one frame. It's rare for one person at a laptop.

## Conclusion

Nothing measured here is worth changing. The loop is close to the camera's
floor, and the cheap options we tried save 2 to 25 ms.
