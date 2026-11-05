package compare

import (
	"errors"
	"strings"
	"testing"
	"time"

	"howdy-go/internal/config"
	"howdy-go/internal/face"
	"howdy-go/internal/imgproc"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// fakeCamera hands out frames in turn, each one taking 100 ms
type fakeCamera struct {
	clock    *clock
	frames   []*imgproc.Gray
	i        int
	err      error
	exposure []int
}

func (c *fakeCamera) Read(time.Duration) (*imgproc.Gray, error) {
	if c.err != nil {
		return nil, c.err
	}
	c.clock.t = c.clock.t.Add(100 * time.Millisecond)
	f := c.frames[c.i%len(c.frames)]
	c.i++
	cp := *f
	cp.Pix = append([]byte(nil), f.Pix...)
	return &cp, nil
}

func (c *fakeCamera) SetExposure(v int) error {
	c.exposure = append(c.exposure, v)
	return nil
}

// fakeFaces sees a face in any frame whose first pixel is bright after
// CLAHE (200 going in), with a
// descriptor that depends on the second pixel
type fakeFaces struct {
	faces    int
	detected []int // image widths seen by Detect
}

func (f *fakeFaces) Detect(g *imgproc.Gray, upsample int, cnn bool) ([]face.Rect, error) {
	f.detected = append(f.detected, g.W)
	if g.Pix[0] < 180 {
		return nil, nil
	}
	n := max(f.faces, 1)
	return make([]face.Rect, n), nil
}

func (f *fakeFaces) Encode(g *imgproc.Gray, r face.Rect) (face.Encoding, error) {
	var e face.Encoding
	e.Descriptor[0] = float64(g.Pix[1]) / 100
	return e, nil
}

func (f *fakeFaces) Close() {}

// A frame that isn't dark after CLAHE, tagged with two marker pixels
func lit(w, h int, first, second byte) *imgproc.Gray {
	g := imgproc.NewGray(w, h)
	for i := range g.Pix {
		g.Pix[i] = byte(100 + i%50)
	}
	g.Pix[0], g.Pix[1] = first, second
	return g
}

func black(w, h int) *imgproc.Gray { return imgproc.NewGray(w, h) }

func settings() *config.Settings {
	return &config.Settings{Certainty: 3.5, RequiredMatches: 1, Timeout: 1, MaxHeight: 360,
		FrameWidth: -1, FrameHeight: -1, DeviceFPS: -1, DarkThreshold: 60, DetectionUpsample: 0,
		EnrollFrames: 15, Exposure: -1}
}

func newLoop(s *config.Settings, frames ...*imgproc.Gray) (*Loop, *fakeCamera, *fakeFaces, *strings.Builder) {
	c := &clock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	cam := &fakeCamera{clock: c, frames: frames}
	faces := &fakeFaces{}
	out := &strings.Builder{}
	known := [][]float64{make([]float64, 128), make([]float64, 128)}
	known[1][0] = 0.2
	return &Loop{
		Settings: s, Known: known, Owner: []int{0, 1}, Labels: []string{"first", "second"},
		Camera: cam, Faces: faces, Out: out, Now: c.now,
		Timings: Timings{Start: c.t},
	}, cam, faces, out
}

func TestMatch(t *testing.T) {
	s := settings()
	s.EndReport = true
	// Distance 0.21 from the second stored encoding, 0.01 away
	l, _, _, out := newLoop(s, lit(64, 36, 0, 0), lit(64, 36, 200, 21))
	if code := l.Run(); code != Success {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"Time spent\n", "  Native: 36x64\n", "  Used: 36x64\n",
		"Frames searched: 2 (10.00 fps)\n", "Black frames ignored: 0 \n",
		"Certainty of winning frame: 0.100\n", "Winning model: 1 (\"second\")\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("end report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out.String(), "Frames that matched") {
		t.Error("matched frame count shown for required_matches 1")
	}
}

func TestExactZeroRejected(t *testing.T) {
	l, _, _, _ := newLoop(settings(), lit(64, 36, 200, 0))
	if code := l.Run(); code != Timeout {
		t.Errorf("a distance of exactly 0 gave exit %d", code)
	}
}

func TestRequiredMatches(t *testing.T) {
	s := settings()
	s.RequiredMatches = 2
	s.EndReport = true
	l, cam, faces, out := newLoop(s, lit(64, 36, 200, 21))
	// Two faces in one frame still count as one frame
	faces.faces = 2
	if code := l.Run(); code != Success {
		t.Fatalf("exit %d", code)
	}
	if cam.i != 2 {
		t.Errorf("matched after %d frames, want 2", cam.i)
	}
	if !strings.Contains(out.String(), "Frames that matched: 2\n") {
		t.Errorf("report:\n%s", out)
	}
}

func TestTimeout(t *testing.T) {
	l, cam, _, _ := newLoop(settings(), lit(64, 36, 200, 90))
	if code := l.Run(); code != Timeout {
		t.Errorf("exit %d", code)
	}
	// 1 s timeout, 100 ms per frame
	if cam.i != 11 {
		t.Errorf("read %d frames", cam.i)
	}
}

func TestTooDark(t *testing.T) {
	s := settings()
	s.DarkThreshold = 0
	l, _, _, out := newLoop(s, lit(64, 36, 200, 21))
	if code := l.Run(); code != TooDark {
		t.Fatalf("exit %d", code)
	}
	if !strings.HasPrefix(out.String(), "All frames were too dark, please check dark_threshold in config\nAverage darkness: ") ||
		!strings.HasSuffix(out.String(), ", Threshold: 0.0\n") {
		t.Errorf("message:\n%s", out)
	}
}

func TestAllBlack(t *testing.T) {
	l, _, _, out := newLoop(settings(), black(64, 36))
	if code := l.Run(); code != TooDark {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "Average darkness: 0.0, Threshold: 60.0\n") {
		t.Errorf("message:\n%s", out)
	}
}

func TestCameraFails(t *testing.T) {
	l, cam, _, out := newLoop(settings(), lit(64, 36, 0, 0))
	cam.err = errors.New("gone")
	if code := l.Run(); code != BadDevice {
		t.Errorf("exit %d", code)
	}
	if out.String() != "Failed to read camera specified in the 'device_path' config option, aborting\n" {
		t.Errorf("message %q", out)
	}
}

func TestShrinkOnly(t *testing.T) {
	s := settings()
	s.MaxHeight = 18
	l, _, faces, _ := newLoop(s, lit(64, 36, 0, 0))
	l.Run()
	if faces.detected[0] != 32 {
		t.Errorf("detector saw width %d, want 32", faces.detected[0])
	}

	s = settings()
	s.MaxHeight = 720
	l, _, faces, _ = newLoop(s, lit(64, 36, 0, 0))
	l.Run()
	if faces.detected[0] != 64 {
		t.Errorf("small frame was enlarged to %d", faces.detected[0])
	}
}

func TestRotate(t *testing.T) {
	s := settings()
	s.Rotate = 1
	l, _, faces, _ := newLoop(s, lit(64, 36, 0, 0))
	l.Run()
	if got := faces.detected[:3]; got[0] != 36 || got[1] != 36 || got[2] != 64 {
		t.Errorf("widths %v, want 36 36 64", got)
	}
}

func TestExposure(t *testing.T) {
	s := settings()
	s.Exposure = 150
	l, cam, _, _ := newLoop(s, lit(64, 36, 0, 0), black(64, 36))
	l.Run()
	// Set after lit frames only, not after black ones
	if len(cam.exposure) != 6 || cam.exposure[0] != 150 {
		t.Errorf("exposure set %v", cam.exposure)
	}
}

func TestSnapshots(t *testing.T) {
	s := settings()
	s.SaveFailed = true
	l, _, _, _ := newLoop(s, black(64, 36), lit(64, 36, 0, 0))
	var got [][]string
	var frames int
	l.Snapshot = func(f []*imgproc.Gray, lines []string) {
		frames = len(f)
		got = append(got, lines)
	}
	l.Hostname = "host"
	l.Run()
	if len(got) != 1 || frames != 3 {
		t.Fatalf("%d snapshots of %d frames", len(got), frames)
	}
	want := []string{"FAILED LOGIN", "Date: 2026/01/02 03:04:06 UTC", "Scan time: 1.1s",
		"Frames: 12 (10.91FPS)", "Hostname: host", "Best certainty value: 100"}
	for i := range want {
		if got[0][i] != want[i] {
			t.Errorf("line %d: %q, want %q", i, got[0][i], want[i])
		}
	}
}
