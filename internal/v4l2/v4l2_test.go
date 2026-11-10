package v4l2

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// Sizes printed by a C probe against /usr/include/linux/videodev2.h
func TestLayout(t *testing.T) {
	for name, c := range map[string][2]uintptr{
		"fmtdesc":                 {unsafe.Sizeof(fmtdesc{}), 64},
		"format":                  {unsafe.Sizeof(format{}), 208},
		"pix_format":              {unsafe.Sizeof(pixFormat{}), 48},
		"requestbuffers":          {unsafe.Sizeof(requestbuffers{}), 20},
		"buffer":                  {unsafe.Sizeof(buffer{}), 88},
		"streamparm":              {unsafe.Sizeof(streamparm{}), 204},
		"control":                 {unsafe.Sizeof(control{}), 8},
		"fmtdesc.pixelformat":     {unsafe.Offsetof(fmtdesc{}.Pixelformat), 44},
		"format.fmt":              {unsafe.Offsetof(format{}.Pix), 8},
		"buffer.bytesused":        {unsafe.Offsetof(buffer{}.Bytesused), 8},
		"buffer.flags":            {unsafe.Offsetof(buffer{}.Flags), 12},
		"buffer.timestamp":        {unsafe.Offsetof(buffer{}.Timestamp), 24},
		"buffer.sequence":         {unsafe.Offsetof(buffer{}.Sequence), 56},
		"buffer.memory":           {unsafe.Offsetof(buffer{}.Memory), 60},
		"buffer.m":                {unsafe.Offsetof(buffer{}.M), 64},
		"buffer.length":           {unsafe.Offsetof(buffer{}.Length), 72},
		"streamparm.timeperframe": {unsafe.Offsetof(streamparm{}.Numerator), 12},
	} {
		if c[0] != c[1] {
			t.Errorf("%s is %d, the kernel says %d", name, c[0], c[1])
		}
	}

	// The size is part of every request number
	for name, c := range map[string][2]uintptr{
		"ENUM_FMT": {vidiocEnumFmt, unsafe.Sizeof(fmtdesc{})},
		"S_FMT":    {vidiocSFmt, unsafe.Sizeof(format{})},
		"REQBUFS":  {vidiocReqbufs, unsafe.Sizeof(requestbuffers{})},
		"DQBUF":    {vidiocDqbuf, unsafe.Sizeof(buffer{})},
		"S_PARM":   {vidiocSParm, unsafe.Sizeof(streamparm{})},
		"S_CTRL":   {vidiocSCtrl, unsafe.Sizeof(control{})},
	} {
		if size := (c[0] >> 16) & 0x3fff; size != c[1] {
			t.Errorf("VIDIOC_%s encodes size %d, struct is %d", name, size, c[1])
		}
	}
}

// fakeDevice behaves like a uvc camera with a queue of frames
type fakeDevice struct {
	formats   []uint32
	width     int
	height    int
	stride    int
	format    uint32
	bufs      [][]byte
	queued    map[uint32]bool
	ready     []readyFrame // filled, waiting to be dequeued
	frames    []readyFrame // what the camera will produce, one per Wait
	streaming bool
	closed    bool
	controls  map[uint32]int32
	fps       uint32
}

type readyFrame struct {
	data  []byte
	flags uint32
	index uint32
}

func (d *fakeDevice) Ioctl(req uint32, arg unsafe.Pointer) error {
	switch req {
	case vidiocEnumFmt:
		f := (*fmtdesc)(arg)
		if int(f.Index) >= len(d.formats) {
			return errors.New("EINVAL")
		}
		f.Pixelformat = d.formats[f.Index]
	case vidiocGFmt:
		f := (*format)(arg)
		f.Pix = pixFormat{Width: uint32(d.width), Height: uint32(d.height), Pixelformat: d.formats[0]}
	case vidiocSFmt:
		f := (*format)(arg)
		if contains(d.formats, f.Pix.Pixelformat) {
			d.format = f.Pix.Pixelformat
		}
		f.Pix.Pixelformat = d.format
		f.Pix.Width, f.Pix.Height = uint32(d.width), uint32(d.height)
		f.Pix.Bytesperline = uint32(d.stride)
	case vidiocSParm:
		d.fps = (*streamparm)(arg).Denominator
	case vidiocReqbufs:
		r := (*requestbuffers)(arg)
		d.queued = map[uint32]bool{}
		d.bufs = make([][]byte, r.Count)
	case vidiocQuerybuf:
		b := (*buffer)(arg)
		b.Length = uint32(d.stride * d.height)
		b.M = uint64(b.Index) * 4096
	case vidiocQbuf:
		b := (*buffer)(arg)
		if d.queued[b.Index] {
			return errors.New("EINVAL: already queued")
		}
		d.queued[b.Index] = true
	case vidiocDqbuf:
		if len(d.ready) == 0 {
			return ErrAgain
		}
		f := d.ready[0]
		d.ready = d.ready[1:]
		b := (*buffer)(arg)
		b.Index = f.index
		b.Bytesused = uint32(len(f.data))
		b.Flags = f.flags
		copy(d.bufs[f.index], f.data)
	case vidiocStreamon:
		d.streaming = true
	case vidiocStreamoff:
		d.streaming = false
	case vidiocSCtrl:
		c := (*control)(arg)
		if d.controls == nil {
			d.controls = map[uint32]int32{}
		}
		d.controls[c.ID] = c.Value
	default:
		return errors.New("unexpected ioctl")
	}
	return nil
}

func (d *fakeDevice) Mmap(offset, length uint32) ([]byte, error) {
	b := make([]byte, length)
	d.bufs[offset/4096] = b
	return b, nil
}

func (d *fakeDevice) Munmap([]byte) error { return nil }

// Each Wait delivers the next scripted frame into a free queued buffer
func (d *fakeDevice) Wait(time.Duration) (bool, error) {
	if len(d.frames) == 0 {
		return false, nil
	}
	f := d.frames[0]
	d.frames = d.frames[1:]
	for i := uint32(0); i < uint32(len(d.bufs)); i++ {
		if d.queued[i] {
			d.queued[i] = false
			f.index = i
			d.ready = append(d.ready, f)
			return true, nil
		}
	}
	return false, errors.New("no buffer queued")
}

func (d *fakeDevice) Close() error {
	d.closed = true
	return nil
}

func frame(w, h, stride int, val byte) []byte {
	b := make([]byte, stride*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			b[y*stride+x] = val + byte(x)
		}
		// Padding past the width must never show up in the image
		for x := w; x < stride; x++ {
			b[y*stride+x] = 0xee
		}
	}
	return b
}

func TestGrey(t *testing.T) {
	d := &fakeDevice{formats: []uint32{0x47504a4d, PixFmtGrey}, width: 4, height: 2, stride: 8}
	c, err := Start(d, 4, 2, 15)
	if err != nil {
		t.Fatal(err)
	}
	if !d.streaming || c.Pixelformat != PixFmtGrey || d.fps != 15 {
		t.Fatalf("not set up right: %+v fps %d", c, d.fps)
	}

	d.frames = []readyFrame{{data: frame(4, 2, 8, 10)}}
	got, err := c.Read(time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{10, 11, 12, 13, 10, 11, 12, 13}; !bytes.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	c.Close()
	if d.streaming || !d.closed {
		t.Error("not released")
	}
}

func TestYUYV(t *testing.T) {
	d := &fakeDevice{formats: []uint32{PixFmtYUYV}, width: 2, height: 1, stride: 4}
	c, err := Start(d, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	d.frames = []readyFrame{{data: []byte{50, 128, 60, 128}}}
	got, err := c.Read(time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{50, 60}) {
		t.Errorf("got %v", got)
	}
}

func TestSkipsBadFrames(t *testing.T) {
	d := &fakeDevice{formats: []uint32{PixFmtGrey}, width: 2, height: 2, stride: 2}
	c, err := Start(d, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	d.frames = []readyFrame{
		{data: []byte{1, 1, 1, 1}, flags: bufFlagError},
		{data: []byte{2, 2}}, // cut short
		{data: []byte{3, 3, 3, 3}},
	}
	got, err := c.Read(time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{3, 3, 3, 3}) {
		t.Errorf("got %v", got)
	}

	// Every buffer went back to the driver
	for i, q := range d.queued {
		if !q {
			t.Errorf("buffer %d not requeued", i)
		}
	}

	if _, err := c.Read(time.Second, nil); !errors.Is(err, ErrTimeout) {
		t.Errorf("no frames: %v", err)
	}
}

func TestNewestWins(t *testing.T) {
	d := &fakeDevice{formats: []uint32{PixFmtGrey}, width: 1, height: 1, stride: 1}
	c, err := Start(d, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Three frames pile up before the reader gets to them
	for _, v := range []byte{7, 8, 9} {
		d.frames = []readyFrame{{data: []byte{v}}}
		if _, err := d.Wait(0); err != nil {
			t.Fatal(err)
		}
	}
	d.frames = []readyFrame{{data: []byte{10}}}
	got, err := c.Read(time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 10 {
		t.Errorf("got %d, want the newest", got[0])
	}
}

func TestUnsupported(t *testing.T) {
	d := &fakeDevice{formats: []uint32{0x47504a4d}, width: 2, height: 2, stride: 2}
	_, err := Start(d, 0, 0, 0)
	if err == nil {
		t.Fatal("MJPEG only camera accepted")
	}
	if !strings.Contains(err.Error(), "only MJPG") {
		t.Errorf("error doesn't say what the camera offers: %v", err)
	}
	if !d.closed {
		t.Error("device left open")
	}
}

func TestExposure(t *testing.T) {
	d := &fakeDevice{formats: []uint32{PixFmtGrey}, width: 1, height: 1, stride: 1}
	c, err := Start(d, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetControl(CIDExposureAuto, ExposureManual); err != nil {
		t.Fatal(err)
	}
	if d.controls[CIDExposureAuto] != 1 {
		t.Error("control not set")
	}
}

// yuyvFrame is a w x h frame with luma 100 and the given U and V
func yuyvFrame(w, h int, u, v byte) []byte {
	b := make([]byte, w*h*2)
	for i := 0; i < len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = 100, u, 100, v
	}
	return b
}

func TestColour(t *testing.T) {
	for _, tt := range []struct {
		name string
		fmt  uint32
		data []byte
		want bool
	}{
		{"YUYV with flat chroma, an IR camera", PixFmtYUYV, yuyvFrame(64, 16, 128, 128), false},
		{"YUYV with a little noise", PixFmtYUYV, yuyvFrame(64, 16, 129, 127), false},
		{"YUYV with colour, a webcam", PixFmtYUYV, yuyvFrame(64, 16, 100, 150), true},
		{"GREY has no chroma", PixFmtGrey, make([]byte, 64*16), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := &fakeDevice{formats: []uint32{tt.fmt}, width: 64, height: 16, stride: len(tt.data) / 16}
			c, err := Start(d, 0, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			d.frames = []readyFrame{{data: tt.data}}
			if _, err := c.Read(time.Second, nil); err != nil {
				t.Fatal(err)
			}
			if got := c.Colour(); got != tt.want {
				t.Errorf("Colour() = %v, want %v", got, tt.want)
			}
		})
	}
}
