// Package compare is the frame loop of howdy-compare, a port of the main
// loop in Howdy's compare.py
package compare

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/fabcj/howdy-go/internal/config"
	"github.com/fabcj/howdy-go/internal/face"
	"github.com/fabcj/howdy-go/internal/imgproc"
	"github.com/fabcj/howdy-go/internal/pyfmt"
)

// Exit codes the PAM module understands (pam/main.hh)
const (
	Success     = 0
	NoFaceModel = 10
	Timeout     = 11
	Abort       = 12
	TooDark     = 13
	BadDevice   = 14
	Rubberstamp = 15
)

const (
	clipLimit   = 2.0
	tiles       = 8
	readTimeout = 2 * time.Second
	snapFrames  = 3
)

// Camera is what the loop reads frames from
type Camera interface {
	Read(timeout time.Duration) (*imgproc.Gray, error)
	SetExposure(value int) error
}

// Timings are the setup times the end report prints
type Timings struct {
	Start   time.Time
	Startup time.Duration // process start to camera and model loading
	Camera  time.Duration
	Models  time.Duration
}

// Loop holds everything one run needs
type Loop struct {
	Settings *config.Settings
	Known    [][]float64 // every stored encoding, flattened
	Owner    []int       // which model each encoding came from
	Labels   []string    // label of each model
	Camera   Camera
	Faces    face.Recognizer
	Match    face.Matcher // backend.MatcherFor(Settings)
	Out      io.Writer    // stdout, where compare.py prints
	Timings  Timings

	// Snapshot gets the first frames and the text lines. nil when snapshots
	// are off or can't be written
	Snapshot func(frames []*imgproc.Gray, lines []string)
	Hostname string

	Now func() time.Time
}

type state struct {
	frames, validFrames      int
	blackTries, darkTries    int
	darkTotal                float32
	sawValid                 bool
	lowest                   float64
	sawFace                  bool
	matchedFrames, lastMatch int
	snaps                    []*imgproc.Gray
	loopStart                time.Time
}

// Run scans frames until a match, the timeout or a camera failure, and
// returns the exit code
func (l *Loop) Run() int {
	s := l.Settings
	now := l.Now
	if now == nil {
		now = time.Now
	}
	st := state{lowest: 10, lastMatch: -1}
	certainty := l.Match.Threshold
	timeout := time.Duration(s.Timeout) * time.Second
	wantSnaps := l.Snapshot != nil && (s.SaveFailed || s.SaveSuccessful)

	st.loopStart = now()
	var scaled struct {
		w, h int
	}
	nativeH, nativeW := 0, 0

	for {
		st.frames++

		if now().Sub(st.loopStart) > timeout {
			if s.SaveFailed {
				l.snapshot(&st, "FAILED", now)
			}
			if st.darkTries == st.validFrames {
				fmt.Fprintln(l.Out, "All frames were too dark, please check dark_threshold in config")
				avg := "0.0"
				if st.sawValid {
					avg = pyfmt.Float32(st.darkTotal / float32(max(1, st.validFrames)))
				}
				fmt.Fprintf(l.Out, "Average darkness: %s, Threshold: %s\n", avg, pyfmt.Float(s.DarkThreshold))
				return TooDark
			}
			return Timeout
		}

		raw, err := l.Camera.Read(readTimeout)
		if err != nil {
			fmt.Fprintln(l.Out, "Failed to read camera specified in the 'device_path' config option, aborting")
			return BadDevice
		}
		if nativeW == 0 {
			nativeW, nativeH = raw.W, raw.H
		}
		gs := imgproc.CLAHE(raw, clipLimit, tiles, tiles)

		if wantSnaps && len(st.snaps) < snapFrames {
			st.snaps = append(st.snaps, raw)
		}

		darkness, total := imgproc.Darkness(gs)
		if total == 0 || darkness == 100 {
			st.blackTries++
			continue
		}
		st.darkTotal += darkness
		st.sawValid = true
		st.validFrames++
		if float64(darkness) > s.DarkThreshold {
			st.darkTries++
			continue
		}

		// Only ever shrink. compare.py also enlarges frames smaller than
		// max_height, which costs time and helps nothing
		height := raw.H
		if s.Rotate == 2 {
			height = raw.W
		}
		if f := scaleFactor(s.MaxHeight, height); f < 1 {
			raw = imgproc.ShrinkArea(raw, f)
			gs = imgproc.ShrinkArea(gs, f)
		}

		switch s.Rotate {
		case 1:
			switch st.frames % 3 {
			case 1:
				raw, gs = imgproc.RotateCCW(raw), imgproc.RotateCCW(gs)
			case 2:
				raw, gs = imgproc.RotateCW(raw), imgproc.RotateCW(gs)
			}
		case 2:
			if st.frames%2 == 0 {
				raw, gs = imgproc.RotateCCW(raw), imgproc.RotateCCW(gs)
			} else {
				raw, gs = imgproc.RotateCW(raw), imgproc.RotateCW(gs)
			}
		}
		scaled.w, scaled.h = raw.W, raw.H

		rects, err := l.Faces.Detect(gs)
		if err != nil {
			fmt.Fprintln(l.Out, "Face detection failed:", err)
			return Abort
		}
		for _, r := range rects {
			enc, err := l.Faces.Encode(raw, r)
			if err != nil {
				fmt.Fprintln(l.Out, "Face encoding failed:", err)
				return Abort
			}
			match, index := face.Best(l.Known, enc.Descriptor)
			if index < 0 {
				return NoFaceModel
			}
			// lowest starts as the int 10 in compare.py, which prints as 100
			if match < st.lowest {
				st.lowest = match
				st.sawFace = true
			}

			if !(0 < match && match < certainty) {
				continue
			}
			// Count each frame once, and keep looking until enough have matched
			if st.lastMatch != st.frames {
				st.lastMatch = st.frames
				st.matchedFrames++
			}
			if st.matchedFrames < s.RequiredMatches {
				continue
			}

			total := now().Sub(l.Timings.Start)
			searching := now().Sub(st.loopStart)
			if s.EndReport {
				l.report(&st, total, searching, nativeW, nativeH, scaled.w, scaled.h, match, index)
			}
			if s.SaveSuccessful {
				l.snapshot(&st, "SUCCESSFUL", now)
			}
			return Success
		}

		if s.Exposure != -1 {
			// Some cameras only take manual exposure if it's set on every frame
			_ = l.Camera.SetExposure(s.Exposure)
		}
	}
}

// compare.py: (max_height / height) or 1
func scaleFactor(maxHeight float64, height int) float64 {
	if height == 0 {
		height = 1
	}
	f := maxHeight / float64(height)
	if f == 0 {
		return 1
	}
	return f
}

func ms(d time.Duration) int {
	return int(math.RoundToEven(d.Seconds() * 1000))
}

func (l *Loop) report(st *state, total, searching time.Duration, nativeW, nativeH, usedW, usedH int, match float64, index int) {
	t := l.Timings
	w := l.Out
	if l.Settings.Rotate == 2 {
		nativeH = nativeW
	}
	fmt.Fprintln(w, "Time spent")
	fmt.Fprintf(w, "  Starting up: %dms\n", ms(t.Startup))
	fmt.Fprintf(w, "  Open cam + load libs: %dms\n", ms(max(t.Camera, t.Models)))
	fmt.Fprintf(w, "    Opening the camera: %dms\n", ms(t.Camera))
	fmt.Fprintf(w, "    Importing recognition libs: %dms\n", ms(t.Models))
	fmt.Fprintf(w, "  Searching for known face: %dms\n", ms(searching))
	fmt.Fprintf(w, "  Total time: %dms\n", ms(total))

	// compare.py prints height x width under both labels
	fmt.Fprintln(w, "\nResolution")
	fmt.Fprintf(w, "  Native: %dx%d\n", nativeH, nativeW)
	fmt.Fprintf(w, "  Used: %dx%d\n", usedH, usedW)

	fmt.Fprintf(w, "\nFrames searched: %d (%.2f fps)\n", st.frames, float64(st.frames)/searching.Seconds())
	fmt.Fprintf(w, "Black frames ignored: %d \n", st.blackTries)
	fmt.Fprintf(w, "Dark frames ignored: %d \n", st.darkTries)
	fmt.Fprintf(w, "%s of winning frame: %.3f\n", l.Match.Unit, l.Match.Shown(match))
	if l.Settings.RequiredMatches > 1 {
		fmt.Fprintf(w, "Frames that matched: %d\n", st.matchedFrames)
	}

	// compare.py indexes the models with the encoding's index, which names
	// the wrong model once one holds several encodings. Use its owner.
	model := index
	label := ""
	if index < len(l.Owner) {
		model = l.Owner[index]
	}
	if model < len(l.Labels) {
		label = l.Labels[model]
	}
	fmt.Fprintf(w, "Winning model: %d (\"%s\")\n", model, label)
}

func (l *Loop) snapshot(st *state, kind string, now func() time.Time) {
	if l.Snapshot == nil || len(st.snaps) == 0 {
		return
	}
	t := now()
	elapsed := t.Sub(st.loopStart).Seconds()
	best := l.Match.NoFace
	bestLine := "Best " + strings.ToLower(l.Match.Unit) + " value: "
	if st.sawFace {
		best = pyfmt.Float(pyfmt.Round(l.Match.Shown(st.lowest), l.Match.Digits))
	}
	fps := "0.0"
	if elapsed > 0 {
		fps = pyfmt.Float(pyfmt.Round(float64(st.frames)/elapsed, 2))
	}
	l.Snapshot(st.snaps, []string{
		kind + " LOGIN",
		"Date: " + t.UTC().Format("2006/01/02 15:04:05") + " UTC",
		"Scan time: " + pyfmt.Float(pyfmt.Round(elapsed, 2)) + "s",
		fmt.Sprintf("Frames: %d (%sFPS)", st.frames, fps),
		"Hostname: " + l.Hostname,
		bestLine + best,
	})
}
