# Contributing

Bug reports and pull requests are welcome. If something could let someone in
who shouldn't get in, report it through [SECURITY.md](SECURITY.md) instead.

## Layout

    cmd/<program>/        one directory per binary, each a thin main
    internal/<package>/   everything else; there is no public Go API
    packaging/            the PKGBUILDs (see below)
    tools/                Python and shell used for evaluation, not shipped
    docs/                 measurements and design notes

The module is `github.com/fabcj/howdy-go`. Code goes in `internal/`. `cmd/`
only connects the pieces, so the code can be tested without a camera. The
README's Contributing section says what each package is for.

## Building and checking

    make                  build into bin/, with the flags the packages use
    make check            go vet, gofmt, tests, and PIE/RELRO/BIND_NOW on the binaries
    make lint             golangci-lint (.golangci.yml)
    make tools            the development programs, below
    make DESTDIR=pkg install

The Makefile holds the install paths. It passes them to the build as `-X`
flags on `internal/paths`, along with the version. Both PKGBUILDs call the
Makefile, so change paths there, not in a PKGBUILD. You need Go, pkgconf,
dlib, and ONNX Runtime. `go test -tags "nodlib noonnx" ./...` runs the tests
that don't need the last two.

Tests that need face data aren't part of `go test ./...`. The README explains
how to run the dlib parity test. Any change to `internal/dlib`,
`internal/sface`, `internal/imgproc`, or the compare loop has to keep the
parity and reference tests passing. The numbers must stay the same as
OpenCV's and dlib's.

## Development programs

`cmd/face-capture` records IR frames for `tools/face-eval.py`. `cmd/v4l2bench`
times a camera from open to first frame. `make tools` builds them into
`bin/`, but nothing installs them. Frames are biometric data. Keep them
outside the repository (`.gitignore` refuses the usual formats), and don't
attach them to issues.

`cmd/save-frontal-detector` is different: the package step runs it.

## Style

Code should pass `gofmt`, `go vet`, and `golangci-lint`. Comments should say
why, in plain sentences, and new code should look like the code around it.
Errors that decide authentication fail closed: they lead to a refusal and the
password prompt, never a success. New behaviour gets a test next to it.

## Packaging

| directory | what |
|---|---|
| `packaging/arch` | builds the committed state of this checkout, for trying a change |
| `packaging/aur/howdy-go-git` | the AUR `howdy-go-git` package, the default branch |
| `packaging/aur/howdy-go` | the AUR `howdy-go` package, a tagged release |

Each AUR directory is its own AUR repository, so the patches and the
`.install` file in them are copies. CI checks that they match. After any
PKGBUILD change, run:

    makepkg --printsrcinfo > .SRCINFO

Then run `namcap` on the PKGBUILD and on the built package.

## Releases

Versions are `vMAJOR.MINOR.PATCH` tags on the default branch. The `-git`
package takes its `pkgver` from the latest tag. To release:

1. Tag and push `vX.Y.Z`.
2. In `packaging/aur/howdy-go`: set `pkgver`, run `updpkgsums`, then
   `makepkg --printsrcinfo > .SRCINFO`, and build it once in a clean chroot.
3. Push that directory to the `howdy-go` AUR repository.
