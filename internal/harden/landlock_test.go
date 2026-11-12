package harden

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const lockChildEnv = "HOWDY_HARDEN_LOCK_CHILD"

// The lock is for life, so it's tried in a child: this test binary run
// again with the env set, which locks itself and reports what it can
// still do through its exit code
func TestLockFilesystem(t *testing.T) {
	if dir := os.Getenv(lockChildEnv); dir != "" {
		if err := LockFilesystem(dir, SnapshotAccess); err != nil {
			if errors.Is(err, ErrNoLandlock) {
				os.Exit(3)
			}
			os.Exit(4)
		}
		if _, err := os.ReadFile("/etc/passwd"); err == nil {
			os.Exit(5)
		}
		if _, err := os.ReadDir("/"); err == nil {
			os.Exit(6)
		}
		f, err := os.OpenFile(filepath.Join(dir, "snap.jpg"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
		if err != nil {
			os.Exit(7)
		}
		f.Close()
		os.Exit(0)
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockFilesystem$")
	cmd.Env = append(os.Environ(), lockChildEnv+"="+dir)
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit) && exit.ExitCode() == 3:
		t.Skip("no Landlock on this kernel")
	case errors.As(err, &exit):
		t.Fatalf("child exited %d: 4 lock failed, 5 read /etc/passwd, 6 listed /, 7 couldn't write in the allowed dir", exit.ExitCode())
	default:
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "snap.jpg")); err != nil {
		t.Error("the allowed directory got no file")
	}
}
