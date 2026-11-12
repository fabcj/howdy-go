# Builds howdy-go the way the PKGBUILDs do, so a local build, CI and the
# packages share one set of flags and install paths.
#
#   make                          build into bin/
#   make test                     unit tests, with dlib and ONNX Runtime
#   make check                    vet, gofmt, tests and the hardening check
#   make DESTDIR=pkg install      what package() runs
#
# makepkg's CPPFLAGS, CFLAGS and LDFLAGS reach cgo through CGO_*, which the
# PKGBUILDs export. Without them, a local build still gets full RELRO.
export CGO_LDFLAGS ?= -Wl,-z,relro -Wl,-z,now

MODULE := github.com/fabcj/howdy-go

GO ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

DESTDIR :=
PREFIX ?= /usr
SYSCONFDIR ?= /etc
LOCALSTATEDIR ?= /var
BINDIR ?= $(PREFIX)/bin
# Where howdy-compare lives, which pam_howdy.so is built to run
LIBEXECDIR ?= $(PREFIX)/lib/howdy-go
DATADIR ?= $(PREFIX)/share

# Where the programs look for their files, see internal/paths
CONFIGDIR ?= $(SYSCONFDIR)/howdy
PATHS := \
	configDir=$(CONFIGDIR) \
	userModelsDir=$(CONFIGDIR)/models \
	sFaceModelsDir=$(CONFIGDIR)/models-sface \
	pinDir=$(CONFIGDIR)/pin \
	pinUserDir=$(CONFIGDIR)/pin-user \
	dlibDataDir=$(DATADIR)/dlib-data \
	onnxDir=$(DATADIR)/howdy-go/onnx \
	dataDir=$(DATADIR)/howdy \
	logPath=$(LOCALSTATEDIR)/log/howdy \
	modelOwner=0

FLAGS ?= -buildmode=pie -trimpath -mod=readonly -modcacherw
GO_LDFLAGS := -linkmode=external \
	-X $(MODULE)/internal/cli.Version=$(VERSION) \
	$(foreach p,$(PATHS),-X $(MODULE)/internal/paths.$(p))

# Installed programs, the build-time helper the package step runs, and the
# development programs (README, Contributing) that nothing installs
PROGRAMS := howdy howdy-compare
TOOLS := save-frontal-detector
DEVTOOLS := face-capture v4l2bench

.PHONY: all build tools test vet fmt-check lint check check-hardening install clean

all: build

# Always runs; go's own build cache makes an unchanged tree fast
build:
	$(GO) build $(FLAGS) -ldflags "$(GO_LDFLAGS)" -o bin/ \
		$(addprefix ./cmd/,$(PROGRAMS) $(TOOLS))

# Not part of build: they record camera frames and time cameras
tools:
	$(GO) build $(FLAGS) -o bin/ $(addprefix ./cmd/,$(DEVTOOLS))

test:
	$(GO) test -mod=readonly -modcacherw ./...

vet:
	$(GO) vet ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo 'gofmt: run gofmt -w on these'; exit 1; }

lint:
	golangci-lint run ./...

check: build vet fmt-check test check-hardening

# PIE, full RELRO and BIND_NOW on what build made, or the hardening flags
# didn't make it through
check-hardening:
	@for b in $(addprefix bin/,$(PROGRAMS)); do \
		readelf -h "$$b" | grep -q 'Type:.*DYN' || { echo "$$b isn't PIE"; exit 1; }; \
		readelf -l "$$b" | grep -q GNU_RELRO || { echo "$$b has no RELRO"; exit 1; }; \
		readelf -d "$$b" | grep -q 'BIND_NOW\|FLAGS_1.*NOW' || { echo "$$b has no BIND_NOW"; exit 1; }; \
	done

install:
	install -Dm755 bin/howdy -t $(DESTDIR)$(BINDIR)
	install -Dm755 bin/howdy-compare -t $(DESTDIR)$(LIBEXECDIR)
	install -Dm644 packaging/sysusers.d/howdy-go.conf $(DESTDIR)$(PREFIX)/lib/sysusers.d/howdy-go.conf

clean:
	rm -rf bin
