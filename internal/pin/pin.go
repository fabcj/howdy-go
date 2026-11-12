// Package pin is the second factor asked for after a face match: a 4 digit
// PIN, kept as a yescrypt hash in a file only root can read. The PAM module
// checks it with the same libcrypt this package makes the hash with.
//
// 4 digits are 10,000 guesses, so the hash only slows down someone who has
// the file. What protects the PIN is that it is asked for after a face match
// and only 3 times, and that the file is out of reach of anyone but root.
package pin

/*
#cgo LDFLAGS: -lcrypt
#include <crypt.h>
#include <stdlib.h>
#include <string.h>

// Hashes pin with a fresh yescrypt salt into out. Returns 0 on success.
static int hd_pin_hash(const char *pin, const unsigned char *rnd, int rndlen, char *out, int outlen) {
	char setting[CRYPT_GENSALT_OUTPUT_SIZE];
	if (crypt_gensalt_rn("$y$", 0, (const char *)rnd, rndlen, setting, sizeof setting) == NULL) {
		return -1;
	}
	struct crypt_data *data = calloc(1, sizeof *data);
	if (data == NULL) {
		return -1;
	}
	char *h = crypt_rn(pin, setting, data, sizeof *data);
	int rc = -1;
	if (h != NULL && h[0] != '*' && (int)strlen(h) < outlen) {
		strcpy(out, h);
		rc = 0;
	}
	explicit_bzero(data, sizeof *data);
	free(data);
	return rc;
}

// Hashes pin with the salt and parameters of hash, and returns 1 if it comes
// out the same
static int hd_pin_check(const char *pin, const char *hash) {
	struct crypt_data *data = calloc(1, sizeof *data);
	if (data == NULL) {
		return 0;
	}
	char *h = crypt_rn(pin, hash, data, sizeof *data);
	int ok = 0;
	if (h != NULL && h[0] != '*' && strlen(h) == strlen(hash)) {
		unsigned char diff = 0;
		for (size_t i = 0; i < strlen(hash); i++) {
			diff |= (unsigned char)(h[i] ^ hash[i]);
		}
		ok = diff == 0;
	}
	explicit_bzero(data, sizeof *data);
	free(data);
	return ok;
}
*/
import "C"

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/fabcj/howdy-go/internal/username"
)

// Length is how many digits a PIN has
const Length = 4

// MaxFails is how many wrong PINs in a row, over any number of face scans,
// switch the PIN off until it is set again. It has to equal PIN_MAX_FAILS in
// the PAM module (pam-pin.patch), which keeps the count.
const MaxFails = 6

// Valid says whether s is a PIN: exactly 4 ASCII digits
func Valid(s string) bool {
	if len(s) != Length {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Hash returns a yescrypt hash of the PIN, as crypt(3) writes it
func Hash(pin string) (string, error) {
	if !Valid(pin) {
		return "", errors.New("a PIN is exactly 4 digits")
	}
	rnd := make([]byte, 16)
	if _, err := rand.Read(rnd); err != nil {
		return "", err
	}
	cpin := C.CString(pin)
	defer C.free(unsafe.Pointer(cpin))
	out := make([]C.char, C.CRYPT_OUTPUT_SIZE)
	if C.hd_pin_hash(cpin, (*C.uchar)(unsafe.Pointer(&rnd[0])), C.int(len(rnd)), &out[0], C.int(len(out))) != 0 {
		return "", errors.New("libcrypt can't make a yescrypt hash")
	}
	h := C.GoString(&out[0])
	if !strings.HasPrefix(h, "$y$") {
		return "", errors.New("libcrypt didn't make a yescrypt hash")
	}
	return h, nil
}

// Verify says whether pin is the PIN hash was made from
func Verify(pin, hash string) bool {
	if !Valid(pin) || !strings.HasPrefix(hash, "$y$") {
		return false
	}
	cpin, chash := C.CString(pin), C.CString(hash)
	defer C.free(unsafe.Pointer(cpin))
	defer C.free(unsafe.Pointer(chash))
	return C.hd_pin_check(cpin, chash) == 1
}

// Path is where a user's PIN hash lives
func Path(dir, user string) (string, error) {
	if !username.Valid(user) {
		return "", fmt.Errorf("%q isn't a valid username", user)
	}
	return filepath.Join(dir, user), nil
}

// Exists says whether the user has a PIN file
func Exists(dir, user string) bool {
	p, err := Path(dir, user)
	if err != nil {
		return false
	}
	st, err := os.Lstat(p)
	return err == nil && st.Mode().IsRegular()
}

// Save writes the hash to a file only root can read or write, in a
// directory only root can enter, and clears the wrong tries counted for the
// PIN before it. It goes to a temporary file first so a crash can't leave a
// half written hash.
func Save(dir, user, hash string) error {
	path, err := Path(dir, user)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	if err := writeOwned(path, hash+"\n", 0, 0); err != nil {
		return err
	}
	return ResetFails(dir, user)
}

// SaveUser is Save for the user store, which is checked by PAM that runs as
// the user, a lock screen. The files belong to the user (uid, gid) so that
// process can read them and keep the count, in a directory that belongs to
// root and that nobody else can write to, so only root can make or remove
// them. The counter has to exist beforehand: the user can't create it.
func SaveUser(dir, user, hash string, uid, gid int) error {
	path, err := Path(dir, user)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	if unix.Geteuid() == 0 {
		if err := os.Chown(dir, 0, 0); err != nil {
			return err
		}
	}
	if err := writeOwned(path, hash+"\n", uid, gid); err != nil {
		return err
	}
	return ResetFailsUser(dir, user, uid, gid)
}

// writeOwned writes data to path, atomically, as a file only uid can access
func writeOwned(path, data string, uid, gid int) error {
	tmp := path + ".tmp"
	if err := unix.Unlink(tmp); err != nil && !errors.Is(err, unix.ENOENT) {
		return &fs.PathError{Op: "unlink", Path: tmp, Err: err}
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = unix.Unlink(tmp)
		}
	}()
	if unix.Geteuid() == 0 {
		if err := f.Chown(uid, gid); err != nil {
			return err
		}
	}
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.WriteString(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := unix.Rename(tmp, path); err != nil {
		return &fs.PathError{Op: "rename", Path: tmp, Err: err}
	}
	ok = true
	return nil
}

// Remove deletes the user's PIN and its failure count, and says so if there
// wasn't a PIN
func Remove(dir, user string) error {
	path, err := Path(dir, user)
	if err != nil {
		return err
	}
	if err := ResetFails(dir, user); err != nil {
		return err
	}
	return os.Remove(path)
}

// RemoveUser deletes a PIN of the user store, with its counter
func RemoveUser(dir, user string) error {
	path, err := Path(dir, user)
	if err != nil {
		return err
	}
	counter, _ := failsPath(dir, user)
	if err := os.Remove(counter); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Remove(path)
}

// ResetFailsUser sets the counter of the user store back to zero. It is
// written, not removed: the process that counts can't make the file.
func ResetFailsUser(dir, user string, uid, gid int) error {
	counter, err := failsPath(dir, user)
	if err != nil {
		return err
	}
	return writeOwned(counter, "0\n", uid, gid)
}

func failsPath(dir, user string) (string, error) {
	p, err := Path(dir, user)
	return p + ".fail", err
}

// ResetFails forgets the wrong PINs counted for the user, which turns a PIN
// the module switched off back on
func ResetFails(dir, user string) error {
	path, err := failsPath(dir, user)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Fails is how many wrong PINs in a row the module has counted for the user
func Fails(dir, user string) (int, error) {
	path, err := failsPath(dir, user)
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s doesn't hold a count", path)
	}
	return n, nil
}

// Load reads the hash of a PIN file, which has to be a regular file nobody
// else can touch, as the PAM module insists too
func Load(dir, user string, owner int) (string, error) {
	path, err := Path(dir, user)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	st := fi.Sys().(*syscall.Stat_t)
	if !fi.Mode().IsRegular() || int(st.Uid) != owner || fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s must be a regular file owned by uid %d that only it can access", path, owner)
	}
	data, err := io.ReadAll(io.LimitReader(f, 1024))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
