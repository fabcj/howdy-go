package v4l2

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// fileDevice is a real /dev/video* node
type fileDevice struct {
	fd int
}

// Open opens a camera node without blocking, so a stalled camera can't hang
// authentication, and checks it really is a character device
func Open(path string) (Device, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFCHR {
		unix.Close(fd)
		return nil, fmt.Errorf("%s isn't a character device", path)
	}
	return &fileDevice{fd: fd}, nil
}

func (d *fileDevice) Ioctl(req uint32, arg unsafe.Pointer) error {
	for {
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(d.fd), uintptr(req), uintptr(arg))
		switch errno {
		case 0:
			return nil
		case unix.EINTR:
			continue
		case unix.EAGAIN:
			return ErrAgain
		}
		return errno
	}
}

func (d *fileDevice) Mmap(offset, length uint32) ([]byte, error) {
	return unix.Mmap(d.fd, int64(offset), int(length), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
}

func (d *fileDevice) Munmap(b []byte) error {
	return unix.Munmap(b)
}

func (d *fileDevice) Wait(timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		ms := int(time.Until(deadline).Milliseconds())
		if ms < 0 {
			return false, nil
		}
		fds := []unix.PollFd{{Fd: int32(d.fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, ms)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return false, err
		}
		if n == 0 {
			return false, nil
		}
		if fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 && fds[0].Revents&unix.POLLIN == 0 {
			return false, errors.New("camera went away")
		}
		return true, nil
	}
}

func (d *fileDevice) Close() error {
	return unix.Close(d.fd)
}
