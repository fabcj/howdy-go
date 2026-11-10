# Security policy

howdy-go decides whether PAM lets someone in, and `howdy-compare` runs as
root on every unlock. Please report problems privately.

## Reporting

Use [GitHub's private vulnerability reporting](https://github.com/fabcj/howdy-go/security/advisories/new)
on this repository. Please don't open a public issue or pull request for a
vulnerability. Also, don't post face data, PIN hashes, or camera frames in a
report. A description and the steps to reproduce are enough.

## What counts

These are in scope, because the design says they must never happen:

- Getting in without a matching face (or the PIN, when `pin` is set), other
  than by showing the camera a convincing picture of the user. This is already
  known, and the README covers it: there is no liveness check yet, and this is
  for convenience, not security.
- A way for a non-root user to read or change another user's face model, PIN
  hash, or config.
- `howdy-compare` or `howdy` failing open. A bad config, model, or camera has
  to end in a refusal and the password prompt, never a success.
- Memory-safety or privilege problems in `howdy-compare`, in `howdy`, or in
  the cgo wrappers around dlib, ONNX Runtime, and libcrypt.
- A way to make the ONNX models load without their pinned sha256.

Out of scope: spoofing with photos, screens, or masks; attacks that already
need root; and bugs in upstream Howdy's PAM module or howdy-gtk. Report those
to [boltgolt/howdy](https://github.com/boltgolt/howdy), unless the patches in
`packaging/aur` introduced them.

## Supported versions

The latest tagged release and the default branch.
