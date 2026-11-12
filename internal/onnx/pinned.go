package onnx

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// SFace is 39 MB, the biggest model this loads
const maxModelSize = 64 << 20

// readPinned reads a regular file, not through a symlink, and returns its
// bytes only if they hash to sha256Hex
func readPinned(path, sha256Hex string) ([]byte, error) {
	want, err := hex.DecodeString(sha256Hex)
	if err != nil || len(want) != sha256.Size {
		return nil, errors.New("onnx: bad pinned hash for " + path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s isn't a regular file", path)
	}
	if fi.Size() <= 0 || fi.Size() > maxModelSize {
		return nil, fmt.Errorf("%s has the wrong size", path)
	}
	data := make([]byte, fi.Size())
	if _, err := io.ReadFull(f, data); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if string(sum[:]) != string(want) {
		return nil, fmt.Errorf("%s doesn't match its pinned SHA-256, reinstall it", path)
	}
	return data, nil
}
