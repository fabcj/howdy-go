package model

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"golang.org/x/sys/unix"
)

// Big enough for thousands of models
const maxFileSize = 16 << 20

var userRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]*[$]?$`)

// ValidUser checks a username before it goes into a path
func ValidUser(user string) bool {
	return len(user) <= 32 && userRe.MatchString(user)
}

// Path is where a user's models live
func Path(dir, user string) (string, error) {
	if !ValidUser(user) {
		return "", fmt.Errorf("%q isn't a valid username", user)
	}
	return filepath.Join(dir, user+".dat"), nil
}

// ErrNoModel means the user has no model file
var ErrNoModel = errors.New("no face model")

// Load reads a model file that must be a regular file owned by owner, not a
// symlink, and not writable by anyone else. The models dir is held to the
// same rules.
func Load(dir, user string, owner int) (*Models, error) {
	return LoadSize(dir, user, owner, EncodingSize)
}

// LoadSize is Load for encodings of another length than dlib's. The file
// is opened relative to the directory that was checked, so the checks and
// the read are on the same objects.
func LoadSize(dir, user string, owner, size int) (*Models, error) {
	path, err := Path(dir, user)
	if err != nil {
		return nil, err
	}
	d, err := os.OpenFile(dir, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoModel
	}
	if err != nil {
		return nil, err
	}
	defer d.Close()
	if err := checkOwned(d, owner); err != nil {
		return nil, err
	}

	fd, err := unix.Openat(int(d.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, ErrNoModel
	}
	if err != nil {
		return nil, &fs.PathError{Op: "openat", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	if err := checkOwned(f, owner); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("%s is too big", path)
	}
	return ParseSize(data, size)
}

// checkOwned makes sure an open file or directory belongs to owner and
// nobody else can write to it. It goes by what was opened, not the path.
func checkOwned(f *os.File, owner int) error {
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	st := fi.Sys().(*syscall.Stat_t)
	if !fi.Mode().IsRegular() && !fi.Mode().IsDir() {
		return fmt.Errorf("%s has the wrong file type", f.Name())
	}
	if int(st.Uid) != owner || fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s must be owned by uid %d and not writable by others", f.Name(), owner)
	}
	return nil
}

// Save writes the models readable by root and the user's group only. The
// group needs to read it because lock screens run compare as the user. It
// goes to a temporary file first so a crash can't leave half a model.
func Save(dir, user string, gid int, m *Models) error {
	path, err := Path(dir, user)
	if err != nil {
		return err
	}
	data, err := m.Bytes()
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := unix.Unlink(tmp); err != nil && !errors.Is(err, unix.ENOENT) {
		return &fs.PathError{Op: "unlink", Path: tmp, Err: err}
	}

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0o640)
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
		if err := f.Chown(0, gid); err != nil {
			return err
		}
	}
	// The umask may have taken bits away, never more than 0640 though
	if err := f.Chmod(0o640); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
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
	return syncDir(dir)
}

// Remove deletes the user's model file
func Remove(dir, user string) error {
	path, err := Path(dir, user)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
