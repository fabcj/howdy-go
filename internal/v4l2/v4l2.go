// Package v4l2 reads grey frames straight from a V4L2 camera, a port of
// Howdy's recorders/v4l2_reader.py. Only GREY and YUYV cameras are supported,
// which covers the IR cameras Howdy is used with.
package v4l2

import (
	"errors"
	"fmt"
	"time"
	"unsafe"
)

// Request numbers and constants from linux/videodev2.h (checked against the
// header on x86_64, see layout_test.go)
const (
	vidiocEnumFmt   = 0xc0405602
	vidiocGFmt      = 0xc0d05604
	vidiocSFmt      = 0xc0d05605
	vidiocReqbufs   = 0xc0145608
	vidiocQuerybuf  = 0xc0585609
	vidiocQbuf      = 0xc058560f
	vidiocDqbuf     = 0xc0585611
	vidiocStreamon  = 0x40045612
	vidiocStreamoff = 0x40045613
	vidiocSParm     = 0xc0cc5616
	vidiocSCtrl     = 0xc008561c

	bufTypeVideoCapture = 1
	memoryMmap          = 1
	bufFlagError        = 0x0040

	PixFmtGrey = 0x59455247
	PixFmtYUYV = 0x56595559

	CIDExposureAuto     = 0x009a0901
	CIDExposureAbsolute = 0x009a0902
	ExposureManual      = 1
)

// Number of buffers the driver fills in turn
const bufferCount = 4

type fmtdesc struct {
	Index       uint32
	Type        uint32
	Flags       uint32
	Description [32]byte
	Pixelformat uint32
	MbusCode    uint32
	Reserved    [3]uint32
}

type pixFormat struct {
	Width        uint32
	Height       uint32
	Pixelformat  uint32
	Field        uint32
	Bytesperline uint32
	Sizeimage    uint32
	Colorspace   uint32
	Priv         uint32
	Flags        uint32
	YcbcrEnc     uint32
	Quantization uint32
	XferFunc     uint32
}

// The fmt union holds pointers in other members, so it starts at offset 8
type format struct {
	Type uint32
	_    uint32
	Pix  pixFormat
	_    [200 - 48]byte
}

type requestbuffers struct {
	Count        uint32
	Type         uint32
	Memory       uint32
	Capabilities uint32
	Flags        uint8
	Reserved     [3]uint8
}

type buffer struct {
	Index     uint32
	Type      uint32
	Bytesused uint32
	Flags     uint32
	Field     uint32
	_         uint32
	Timestamp [16]byte
	Timecode  [16]byte
	Sequence  uint32
	Memory    uint32
	M         uint64 // the mmap offset is the low 32 bits
	Length    uint32
	Reserved2 uint32
	RequestFD uint32
	_         uint32
}

type streamparm struct {
	Type         uint32
	Capability   uint32
	Capturemode  uint32
	Numerator    uint32
	Denominator  uint32
	Extendedmode uint32
	Readbuffers  uint32
	Reserved     [4]uint32
	_            [200 - 40]byte
}

type control struct {
	ID    uint32
	Value int32
}

// Device is what a camera needs from the OS, so tests can use a fake one
type Device interface {
	Ioctl(req uint32, arg unsafe.Pointer) error
	Mmap(offset, length uint32) ([]byte, error)
	Munmap([]byte) error
	// Wait blocks until a frame may be ready, false on timeout
	Wait(timeout time.Duration) (bool, error)
	Close() error
}

// ErrAgain is what Ioctl returns for DQBUF when no frame is waiting
var ErrAgain = errors.New("no frame ready")

// ErrTimeout means no usable frame arrived in time
var ErrTimeout = errors.New("timed out waiting for a frame")

// Camera is a streaming camera
type Camera struct {
	dev          Device
	bufs         [][]byte
	streaming    bool
	Width        int
	Height       int
	Bytesperline int
	Pixelformat  uint32
}

// Start configures the device and starts streaming. width, height and fps
// of 0 or less leave the driver's choice.
func Start(dev Device, width, height, fps int) (*Camera, error) {
	c := &Camera{dev: dev}
	if err := c.start(width, height, fps); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Camera) start(width, height, fps int) error {
	// Ask the camera what it has and pick one we can read as grey
	var formats []uint32
	for i := uint32(0); i < 64; i++ {
		d := fmtdesc{Index: i, Type: bufTypeVideoCapture}
		if err := c.dev.Ioctl(vidiocEnumFmt, unsafe.Pointer(&d)); err != nil {
			break
		}
		formats = append(formats, d.Pixelformat)
	}
	var want uint32
	switch {
	case contains(formats, PixFmtGrey):
		want = PixFmtGrey
	case contains(formats, PixFmtYUYV):
		want = PixFmtYUYV
	default:
		return errors.New("camera has no GREY or YUYV format")
	}

	f := format{Type: bufTypeVideoCapture}
	if err := c.dev.Ioctl(vidiocGFmt, unsafe.Pointer(&f)); err != nil {
		return fmt.Errorf("VIDIOC_G_FMT: %w", err)
	}
	f.Pix.Pixelformat = want
	if width > 0 {
		f.Pix.Width = uint32(width)
	}
	if height > 0 {
		f.Pix.Height = uint32(height)
	}
	if err := c.dev.Ioctl(vidiocSFmt, unsafe.Pointer(&f)); err != nil {
		return fmt.Errorf("VIDIOC_S_FMT: %w", err)
	}

	// The driver can pick something else, use whatever it settled on
	c.Pixelformat = f.Pix.Pixelformat
	c.Width = int(f.Pix.Width)
	c.Height = int(f.Pix.Height)
	c.Bytesperline = int(f.Pix.Bytesperline)
	if c.Pixelformat != want {
		return errors.New("camera did not accept the requested format")
	}
	minLine := c.Width
	if want == PixFmtYUYV {
		minLine *= 2
	}
	if c.Width <= 0 || c.Height <= 0 || c.Width > 8192 || c.Height > 8192 || c.Bytesperline < minLine {
		return fmt.Errorf("camera reports an unusable size %dx%d (%d bytes per line)", c.Width, c.Height, c.Bytesperline)
	}

	if fps > 0 {
		p := streamparm{Type: bufTypeVideoCapture, Numerator: 1, Denominator: uint32(fps)}
		_ = c.dev.Ioctl(vidiocSParm, unsafe.Pointer(&p))
	}

	req := requestbuffers{Count: bufferCount, Type: bufTypeVideoCapture, Memory: memoryMmap}
	if err := c.dev.Ioctl(vidiocReqbufs, unsafe.Pointer(&req)); err != nil {
		return fmt.Errorf("VIDIOC_REQBUFS: %w", err)
	}
	if req.Count == 0 || req.Count > 32 {
		return fmt.Errorf("driver gave %d buffers", req.Count)
	}
	for i := uint32(0); i < req.Count; i++ {
		b := c.newBuffer()
		b.Index = i
		if err := c.dev.Ioctl(vidiocQuerybuf, unsafe.Pointer(&b)); err != nil {
			return fmt.Errorf("VIDIOC_QUERYBUF: %w", err)
		}
		mem, err := c.dev.Mmap(uint32(b.M), b.Length)
		if err != nil {
			return fmt.Errorf("mmap: %w", err)
		}
		c.bufs = append(c.bufs, mem)
		if err := c.dev.Ioctl(vidiocQbuf, unsafe.Pointer(&b)); err != nil {
			return fmt.Errorf("VIDIOC_QBUF: %w", err)
		}
	}

	t := int32(bufTypeVideoCapture)
	if err := c.dev.Ioctl(vidiocStreamon, unsafe.Pointer(&t)); err != nil {
		return fmt.Errorf("VIDIOC_STREAMON: %w", err)
	}
	c.streaming = true
	return nil
}

func contains(list []uint32, v uint32) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (c *Camera) newBuffer() buffer {
	return buffer{Type: bufTypeVideoCapture, Memory: memoryMmap}
}

// Take every filled buffer off the queue, hand back all but the newest
func (c *Camera) dequeueNewest() (*buffer, error) {
	var newest *buffer
	for {
		b := c.newBuffer()
		err := c.dev.Ioctl(vidiocDqbuf, unsafe.Pointer(&b))
		if errors.Is(err, ErrAgain) {
			return newest, nil
		}
		if err != nil {
			return nil, fmt.Errorf("VIDIOC_DQBUF: %w", err)
		}
		if newest != nil {
			if err := c.dev.Ioctl(vidiocQbuf, unsafe.Pointer(newest)); err != nil {
				return nil, fmt.Errorf("VIDIOC_QBUF: %w", err)
			}
		}
		newest = &b
	}
}

// Read waits up to timeout in total for the newest good frame and returns
// its grey plane, Width*Height bytes. dst is reused if it's big enough.
//
// The Python reader restarts its timeout on every skipped frame, so a camera
// sending only broken frames could hang it. This one doesn't.
func (c *Camera) Read(timeout time.Duration, dst []byte) ([]byte, error) {
	if !c.streaming {
		return nil, errors.New("camera isn't streaming")
	}
	deadline := time.Now().Add(timeout)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return nil, ErrTimeout
		}
		ready, err := c.dev.Wait(left)
		if err != nil {
			return nil, err
		}
		if !ready {
			return nil, ErrTimeout
		}

		b, err := c.dequeueNewest()
		if err != nil {
			return nil, err
		}
		if b == nil {
			continue
		}
		if int(b.Index) >= len(c.bufs) {
			return nil, fmt.Errorf("driver returned buffer %d", b.Index)
		}

		need := c.Bytesperline * c.Height
		mem := c.bufs[b.Index]
		used := int(b.Bytesused)
		good := b.Flags&bufFlagError == 0 && used >= need && used <= len(mem)
		if good {
			dst = c.copyGrey(mem[:need], dst)
		}
		// The buffer gets refilled once it's queued again
		if err := c.dev.Ioctl(vidiocQbuf, unsafe.Pointer(b)); err != nil {
			return nil, fmt.Errorf("VIDIOC_QBUF: %w", err)
		}
		if good {
			return dst, nil
		}
	}
}

func (c *Camera) copyGrey(src, dst []byte) []byte {
	n := c.Width * c.Height
	if cap(dst) < n {
		dst = make([]byte, n)
	}
	dst = dst[:n]
	for y := 0; y < c.Height; y++ {
		row := src[y*c.Bytesperline:]
		out := dst[y*c.Width : (y+1)*c.Width]
		if c.Pixelformat == PixFmtYUYV {
			// Luma is every other byte
			for x := range out {
				out[x] = row[2*x]
			}
		} else {
			copy(out, row[:c.Width])
		}
	}
	return dst
}

// SetControl sets a camera control, for exposure
func (c *Camera) SetControl(id uint32, value int32) error {
	if !c.streaming {
		return errors.New("camera isn't streaming")
	}
	ctl := control{ID: id, Value: value}
	return c.dev.Ioctl(vidiocSCtrl, unsafe.Pointer(&ctl))
}

// Close stops streaming and releases the device
func (c *Camera) Close() {
	if c.dev == nil {
		return
	}
	if c.streaming {
		t := int32(bufTypeVideoCapture)
		_ = c.dev.Ioctl(vidiocStreamoff, unsafe.Pointer(&t))
		c.streaming = false
	}
	for _, b := range c.bufs {
		_ = c.dev.Munmap(b)
	}
	c.bufs = nil
	_ = c.dev.Close()
	c.dev = nil
}
