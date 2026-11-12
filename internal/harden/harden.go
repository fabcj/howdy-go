// Package harden locks howdy-compare down: no new privileges, no core
// dumps, resource limits, and dropping root when it starts as root.
package harden

/*
#include <sys/prctl.h>
#include <sys/resource.h>

// Runs from the C start code before the Go runtime makes any threads, so
// every thread inherits it. PR_SET_NO_NEW_PRIVS is per thread, and Go can't
// apply a prctl to all its threads in a cgo binary.
__attribute__((constructor)) static void howdy_harden_early(void) {
	struct rlimit none = {0, 0};
	setrlimit(RLIMIT_CORE, &none);
	prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0);
	prctl(PR_SET_DUMPABLE, 0, 0, 0, 0);
}
*/
import "C"

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Limits caps CPU time, and address space at what's mapped now plus
// budget. A fixed cap doesn't work: OpenBLAS and the Go runtime reserve
// 2-4 GB before main depending on the environment, almost none of it used.
func Limits(budget uint64, cpuSeconds uint64) error {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return fmt.Errorf("RLIMIT_AS: %w", err)
	}
	size, err := vmSize(status)
	if err != nil {
		return fmt.Errorf("RLIMIT_AS: %w", err)
	}
	addressSpace := size + budget
	if err := unix.Setrlimit(unix.RLIMIT_AS, &unix.Rlimit{Cur: addressSpace, Max: addressSpace}); err != nil {
		return fmt.Errorf("RLIMIT_AS: %w", err)
	}
	if err := unix.Setrlimit(unix.RLIMIT_CPU, &unix.Rlimit{Cur: cpuSeconds, Max: cpuSeconds}); err != nil {
		return fmt.Errorf("RLIMIT_CPU: %w", err)
	}
	return nil
}

// vmSize reads VmSize out of /proc/self/status, in bytes
func vmSize(status []byte) (uint64, error) {
	for line := range strings.Lines(string(status)) {
		rest, ok := strings.CutPrefix(line, "VmSize:")
		if !ok {
			continue
		}
		kb, ok := strings.CutSuffix(strings.TrimSpace(rest), " kB")
		if !ok {
			break
		}
		n, err := strconv.ParseUint(strings.TrimSpace(kb), 10, 64)
		if err != nil || n > 1<<44 {
			break
		}
		return n << 10, nil
	}
	return 0, errors.New("no VmSize in /proc/self/status")
}

// DropUser is the user to run the frame loop as: howdy, which the package
// creates through sysusers.d, or nobody where that user doesn't exist.
// Looked up in /etc/passwd directly, without NSS.
const DropUser = "howdy"

// Unprivileged returns the uid, gid and name of the user to drop to
func Unprivileged() (uid, gid int, name string, err error) {
	uid, gid, err = lookup("/etc/passwd", DropUser)
	if err == nil {
		return uid, gid, DropUser, nil
	}
	uid, gid, err = lookup("/etc/passwd", "nobody")
	return uid, gid, "nobody", err
}

func lookup(passwd, name string) (uid, gid int, err error) {
	f, err := os.Open(passwd)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Split(s.Text(), ":")
		if len(fields) < 4 || fields[0] != name {
			continue
		}
		uid, err1 := strconv.Atoi(fields[2])
		gid, err2 := strconv.Atoi(fields[3])
		if err1 != nil || err2 != nil || uid <= 0 || gid <= 0 {
			return 0, 0, fmt.Errorf("bad %s entry in %s", name, passwd)
		}
		return uid, gid, nil
	}
	if err := s.Err(); err != nil {
		return 0, 0, err
	}
	return 0, 0, fmt.Errorf("no %s user in %s", name, passwd)
}

// Drop switches every thread to uid and gid with no supplementary groups,
// then checks it took everywhere and can't be undone. The syscall package
// goes through glibc in a cgo binary, which applies these to all threads.
func Drop(uid, gid int) error {
	if err := syscall.Setgroups([]int{}); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := syscall.Setresgid(gid, gid, gid); err != nil {
		return fmt.Errorf("setresgid: %w", err)
	}
	if err := syscall.Setresuid(uid, uid, uid); err != nil {
		return fmt.Errorf("setresuid: %w", err)
	}
	// A uid change resets this, so set it again
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("PR_SET_DUMPABLE: %w", err)
	}
	return Verify(uid, gid)
}

// Verify checks the process runs as uid and gid only, on every thread, with
// no capabilities, no supplementary groups and no_new_privs set
func Verify(uid, gid int) error {
	r, e, s := unix.Getresuid()
	if r != uid || e != uid || s != uid {
		return fmt.Errorf("uids are %d %d %d", r, e, s)
	}
	rg, eg, sg := unix.Getresgid()
	if rg != gid || eg != gid || sg != gid {
		return fmt.Errorf("gids are %d %d %d", rg, eg, sg)
	}
	if uid != 0 && syscall.Setresuid(0, 0, 0) == nil {
		return errors.New("could get root back")
	}

	tasks, err := filepath.Glob("/proc/self/task/*/status")
	if err != nil || len(tasks) == 0 {
		return errors.New("can't list threads")
	}
	want := map[string]string{
		"Uid":        fmt.Sprintf("%d\t%d\t%d\t%d", uid, uid, uid, uid),
		"Gid":        fmt.Sprintf("%d\t%d\t%d\t%d", gid, gid, gid, gid),
		"Groups":     "",
		"NoNewPrivs": "1",
		"CapPrm":     "0000000000000000",
		"CapEff":     "0000000000000000",
		"CapAmb":     "0000000000000000",
	}
	for _, path := range tasks {
		data, err := os.ReadFile(path)
		if err != nil {
			// The thread may have just exited
			continue
		}
		seen := 0
		for line := range strings.Lines(string(data)) {
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			w, check := want[k]
			if !check {
				continue
			}
			if strings.TrimSpace(v) != w {
				return fmt.Errorf("%s: %s is %q", path, k, strings.TrimSpace(v))
			}
			seen++
		}
		if seen != len(want) {
			return fmt.Errorf("%s: missing fields", path)
		}
	}
	return nil
}

// CheckNoNewPrivs makes sure every thread has no_new_privs, which the
// constructor above sets. It fails if the binary was linked so that C
// constructors didn't run.
func CheckNoNewPrivs() error {
	tasks, err := filepath.Glob("/proc/self/task/*/status")
	if err != nil || len(tasks) == 0 {
		return errors.New("can't list threads")
	}
	for _, path := range tasks {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if !strings.Contains(string(data), "\nNoNewPrivs:\t1\n") {
			return fmt.Errorf("%s: no_new_privs isn't set", path)
		}
	}
	return nil
}
