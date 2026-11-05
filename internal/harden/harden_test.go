package harden

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestEarly(t *testing.T) {
	if err := CheckNoNewPrivs(); err != nil {
		t.Fatal(err)
	}
	var r unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_CORE, &r); err != nil || r.Cur != 0 || r.Max != 0 {
		t.Errorf("core limit %+v, %v", r, err)
	}
	if d, _ := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0); d != 0 {
		t.Errorf("dumpable is %d", d)
	}
}

func TestLookup(t *testing.T) {
	p := filepath.Join(t.TempDir(), "passwd")
	os.WriteFile(p, []byte("root:x:0:0::/root:/bin/sh\nnobody:x:65534:65534:Kernel Overflow User:/:/usr/bin/nologin\n"), 0o600)
	uid, gid, err := lookup(p, "nobody")
	if err != nil || uid != 65534 || gid != 65534 {
		t.Errorf("got %d %d %v", uid, gid, err)
	}
	if _, _, err := lookup(p, "root"); err == nil {
		t.Error("uid 0 accepted as the drop target")
	}
	if _, _, err := lookup(p, "missing"); err == nil {
		t.Error("missing user found")
	}
}

func TestVMSize(t *testing.T) {
	got, err := vmSize([]byte("Name:\thowdy-compare\nVmPeak:\t 1850000 kB\nVmSize:\t 1784372 kB\nVmLck:\t       0 kB\n"))
	if err != nil || got != 1784372<<10 {
		t.Errorf("got %d %v", got, err)
	}
	for _, bad := range []string{"", "VmPeak:\t 1 kB\n", "VmSize:\t 12 MB\n", "VmSize:\t -1 kB\n", "VmSize:\t 99999999999999999999 kB\n"} {
		if _, err := vmSize([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Skip(err)
	}
	if n, err := vmSize(status); err != nil || n == 0 {
		t.Errorf("own status: %d %v", n, err)
	}
}

func TestVerifyAsUser(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	// Our own ids pass only if there are no supplementary groups, so just
	// check a wrong uid is caught
	if err := Verify(os.Getuid()+1, os.Getgid()); err == nil {
		t.Error("wrong uid passed")
	}
}
