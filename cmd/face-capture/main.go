// face-capture records lit IR frames from the configured camera for
// tools/face-eval.py. Frames are biometric data: they go to a private
// directory outside the repo and are never sent anywhere.
//
//	face-capture [-n frames] [-every 0s] [-dark 95] [-device /dev/videoN] [-dir path] label
//
// label names whose face it is: your login for genuine frames, anything
// else for impostors, and attack-<what> for photos or screens held up to
// the camera. Each run is one session, in <dir>/<label>/<date-time>/, with
// the frames as .pgm at the camera's own size and a meta.json that has when
// each one arrived, so the harness can tell how long an unlock would take.
//
// Which frames compare would skip as too dark is up to the harness
// (--dark-threshold), so frames are kept whatever config.ini's
// dark_threshold says. Only the emitter's off phase is dropped: frames more
// than -dark percent near-black after CLAHE, which is 98-99 on a strobing
// emitter, against 50-70 for lit frames.
//
// -keep-dark also keeps, with each lit frame, the off-phase frame the camera
// sent just before it, in <session>/dark/. A real face is lit in one and
// dark in the other while a screen glows in both, which is what
// tools/liveness-eval.py measures. The dark frames show a face too, so they
// get the same care as the others.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"time"

	"golang.org/x/sys/unix"

	"github.com/fabcj/howdy-go/internal/camera"
	"github.com/fabcj/howdy-go/internal/config"
	"github.com/fabcj/howdy-go/internal/imgproc"
	"github.com/fabcj/howdy-go/internal/paths"
)

var labelRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type meta struct {
	Label             string    `json:"label"`
	Started           string    `json:"started"`
	Device            string    `json:"device"`
	Width             int       `json:"width"`
	Height            int       `json:"height"`
	MaxHeight         float64   `json:"max_height"`
	DarkThreshold     float64   `json:"dark_threshold"` // config.ini's, not applied here
	KeepBelow         float64   `json:"keep_below_darkness"`
	DetectionUpsample int       `json:"detection_upsample"`
	Certainty         float64   `json:"certainty"`
	Rotate            int       `json:"rotate"`
	Exposure          int       `json:"exposure"`
	Kept              int       `json:"kept"`
	Dark              int       `json:"dark"`
	Frames            int       `json:"frames"`
	TimesMS           []int64   `json:"times_ms"` // per kept frame, since recording started
	Darkness          []float32 `json:"darkness"` // per kept frame, as compare computes it

	// With -keep-dark: each kept frame's number in the camera's stream, and
	// the number of the off-phase frame saved as dark/<number>.pgm just
	// before it, -1 when the one before it was lit too
	Seq     []int `json:"seq,omitempty"`
	DarkSeq []int `json:"dark_seq,omitempty"`
}

func main() {
	n := flag.Int("n", 60, "lit frames to keep")
	every := flag.Duration("every", 0, "least time between kept frames, 0 keeps every lit one")
	dark := flag.Float64("dark", 95, "darkness (percent of near-black pixels after CLAHE) above which a frame is the emitter's off phase")
	keepDark := flag.Bool("keep-dark", false, "also keep the off-phase frame before each lit one, for tools/liveness-eval.py")
	wait := flag.Duration("wait", 5*time.Second, "time to get in position, and for anyone else to leave the picture")
	device := flag.String("device", "", "camera, instead of device_path in config.ini")
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		cache = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	dir := flag.String("dir", filepath.Join(cache, "howdy-go", "faces"), "where sessions go")
	flag.Parse()
	if flag.NArg() != 1 || !labelRe.MatchString(flag.Arg(0)) || *n < 1 || !(*dark > 0 && *dark <= 100) {
		fmt.Fprintln(os.Stderr, "usage: face-capture [-n frames] [-every 0s] [-dark 95] [-keep-dark] [-wait 3s] [-device /dev/videoN] [-dir path] label")
		fmt.Fprintln(os.Stderr, "label: lowercase letters, digits, _ and -, like me, guest1 or attack-phone")
		os.Exit(2)
	}
	if err := run(flag.Arg(0), *dir, *device, *n, *dark, *every, *wait, *keepDark); err != nil {
		fmt.Fprintln(os.Stderr, "face-capture:", err)
		os.Exit(1)
	}
}

func run(label, root, device string, n int, keepBelow float64, every, wait time.Duration, keepDark bool) error {
	// Root would leave root-owned frames in a user's cache. The video group
	// is enough to open the camera.
	if os.Geteuid() == 0 {
		return errors.New("run this as your own user, in the video group, not as root")
	}
	f, err := config.Read(paths.ConfigFile())
	if err != nil {
		return err
	}
	s, err := config.Load(f)
	if err != nil {
		return err
	}
	if device != "" {
		s.DevicePath = device
	}

	started := time.Now()
	session := filepath.Join(root, label, started.Format("20060102-150405"))
	if err := os.MkdirAll(filepath.Dir(session), 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(session, 0o700); err != nil {
		return err
	}
	dfd, err := unix.Open(session, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(dfd)

	cam, err := camera.Open(s)
	if err != nil {
		return err
	}
	defer cam.Close()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	fmt.Fprintf(os.Stderr, "Recording %d frames of %s from %s\n", n, label, s.DevicePath)
	fmt.Fprintln(os.Stderr, "Only "+label+" should be in the picture, sitting the way they unlock and moving a little.")
	fmt.Fprintln(os.Stderr, "Anyone else in a frame spoils the numbers. Ctrl+C stops early.")
	// A countdown, so whoever started it has time to get out of the picture
	for left := wait; left > 0; left -= time.Second {
		fmt.Fprintf(os.Stderr, "\rstarting in %d s ", int((left+time.Second-1)/time.Second))
		select {
		case <-time.After(min(left, time.Second)):
		case <-stop:
			fmt.Fprintln(os.Stderr)
			return os.Remove(session)
		}
	}
	fmt.Fprintln(os.Stderr, "\rrecording      ")

	m := meta{
		Label: label, Started: started.Format(time.RFC3339), Device: s.DevicePath,
		MaxHeight: s.MaxHeight, DarkThreshold: s.DarkThreshold, DetectionUpsample: s.DetectionUpsample,
		Certainty: s.Certainty, Rotate: s.Rotate, Exposure: s.Exposure, KeepBelow: keepBelow,
	}
	var last time.Time
	recording := time.Now()
	// The newest off-phase frame, and its number, until a frame follows it
	var off *imgproc.Gray
	offSeq := -1
loop:
	for m.Kept < n {
		select {
		case <-stop:
			break loop
		default:
		}
		raw, err := cam.Read(2 * time.Second)
		if err != nil {
			return err
		}
		at := time.Since(recording)
		m.Frames++
		m.Width, m.Height = raw.W, raw.H
		if s.Exposure != -1 {
			_ = cam.SetExposure(s.Exposure)
		}
		darkness, total := imgproc.Darkness(imgproc.CLAHE(raw, 2.0, 8, 8))
		if total == 0 || float64(darkness) > keepBelow {
			m.Dark++
			off, offSeq = raw, m.Frames
			// Counted here too, so a camera that only gives dark frames shows
			fmt.Fprintf(os.Stderr, "\rkept %d/%d, %d dark (last %.1f%%)  ", m.Kept, n, m.Dark, darkness)
			continue
		}
		if time.Since(last) < every {
			off = nil
			continue
		}
		last = time.Now()
		if err := writePGM(dfd, fmt.Sprintf("%04d.pgm", m.Kept), raw); err != nil {
			return err
		}
		if keepDark {
			pair := -1
			if off != nil && offSeq == m.Frames-1 {
				pair = offSeq
				if err := writeDark(dfd, fmt.Sprintf("%04d.pgm", pair), off); err != nil {
					return err
				}
			}
			m.Seq = append(m.Seq, m.Frames)
			m.DarkSeq = append(m.DarkSeq, pair)
		}
		off = nil
		m.Kept++
		m.TimesMS = append(m.TimesMS, at.Milliseconds())
		m.Darkness = append(m.Darkness, darkness)
		fmt.Fprintf(os.Stderr, "\rkept %d/%d, %d dark (last %.1f%%)  ", m.Kept, n, m.Dark, darkness)
	}
	fmt.Fprintln(os.Stderr)
	if m.Kept == 0 {
		os.Remove(session)
		return fmt.Errorf("no lit frames in %d (is the IR emitter on? -dark %g keeps frames up to that darkness)", m.Frames, keepBelow)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(dfd, "meta.json", append(data, '\n')); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%d frames (%d dark skipped) in %s\n", m.Kept, m.Dark, session)
	return nil
}

// writeDark writes into the session's dark/ directory, which it makes first
func writeDark(dfd int, name string, g *imgproc.Gray) error {
	if err := unix.Mkdirat(dfd, "dark", 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}
	fd, err := unix.Openat(dfd, "dark", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return writePGM(fd, name, g)
}

func writePGM(dfd int, name string, g *imgproc.Gray) error {
	header := fmt.Sprintf("P5\n%d %d\n255\n", g.W, g.H)
	return writeFile(dfd, name, append([]byte(header), g.Pix...))
}

func writeFile(dfd int, name string, data []byte) error {
	fd, err := unix.Openat(dfd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	f := os.NewFile(uintptr(fd), name)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
