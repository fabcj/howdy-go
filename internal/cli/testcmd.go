package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fabcj/howdy-go/internal/backend"
	"github.com/fabcj/howdy-go/internal/compare"
	"github.com/fabcj/howdy-go/internal/config"
	"github.com/fabcj/howdy-go/internal/face"
	"github.com/fabcj/howdy-go/internal/imgproc"
	"github.com/fabcj/howdy-go/internal/model"
)

// test is Howdy's cli/test.py as a terminal readout instead of a window,
// which can't open under sudo on Wayland. One line per frame, then a
// summary on ctrl+C to tune certainty with.
func (c *cmd) test() int {
	_, s, err := c.settings()
	if err != nil {
		return c.errorf("%v", err)
	}
	if msg := backend.MissingData(s.Recognizer); msg != "" {
		c.say(msg)
		return 1
	}

	// Like test.py, a missing model only means no matching
	var known [][]float64
	var owner []int
	var labels []string
	m, err := c.loadModels()
	switch {
	case errors.Is(err, model.ErrNoModel):
		c.printf("No face model known for %s, only showing detection\n", c.user)
	case err != nil:
		return c.errorf("%v", err)
	default:
		known, owner, err = backend.Known(s, c.user, m)
		if errors.Is(err, backend.ErrNotEnrolled) {
			c.printf("No sface model for %s yet (run howdy add), only showing detection\n", c.user)
		} else if err != nil {
			return c.errorf("%v", err)
		}
		for _, e := range m.Entries {
			labels = append(labels, model.Label(e))
		}
	}
	certainty := compare.Threshold(s)
	sim := s.Recognizer == "sface"
	// What's shown for a distance: certainty for dlib, similarity for sface
	shown := func(d float64) float64 {
		if sim {
			return face.Cosine(d)
		}
		return d * 10
	}

	faces, err := c.Faces(s, s.Recognizer)
	if err != nil {
		return c.errorf("can't load the %s models: %v", s.Recognizer, err)
	}
	defer faces.Close()
	cam, closeCam, code := c.openCamera(s)
	if code != 0 {
		return code
	}
	defer closeCam()

	stop, release := c.Interrupts()
	defer release()

	c.printf("\nTesting the camera at %s for %s\n", s.DevicePath, c.user)
	if sim {
		c.printf("Recognizer sface, match threshold %g, dark threshold %g\n", s.MatchThreshold, s.DarkThreshold)
	} else {
		c.printf("Certainty %g, dark threshold %g, upsample %d\n", s.Certainty, s.DarkThreshold, s.DetectionUpsample)
	}
	c.say("Press ctrl+C to stop\n")
	c.say("frame      ms  dark%  faces  rec ms  result")

	var st testStats
	start := c.Now()
	for {
		select {
		case <-stop:
			st.report(c, c.Now().Sub(start), s)
			return 0
		default:
		}

		raw, code := c.readFrame(cam)
		if code != 0 {
			return code
		}
		st.frames++
		at := c.Now().Sub(start)
		gs := imgproc.CLAHE(raw, 2.0, 8, 8)
		darkness, total := imgproc.Darkness(gs)
		line := fmt.Sprintf("%5d  %6d  %5.1f", st.frames, at.Milliseconds(), darkness)

		switch {
		case total == 0 || darkness == 100:
			st.dark++
			c.printf("%s  %5s  %6s  black frame\n", line, "", "")
			continue
		case float64(darkness) > s.DarkThreshold:
			st.dark++
			c.printf("%s  %5s  %6s  dark frame\n", line, "", "")
			continue
		}

		// Shrunk the way compare does it, so the numbers are what an unlock sees
		if f := s.MaxHeight / float64(raw.H); f > 0 && f < 1 {
			raw = imgproc.ShrinkArea(raw, f)
			gs = imgproc.ShrinkArea(gs, f)
		}

		t := c.Now()
		rects, err := faces.Detect(gs)
		if err != nil {
			return c.errorf("face detection failed: %v", err)
		}
		var results []string
		for _, r := range rects {
			if len(known) == 0 {
				results = append(results, "face")
				continue
			}
			enc, err := faces.Encode(raw, r)
			if err != nil {
				return c.errorf("face encoding failed: %v", err)
			}
			d, i := face.Best(known, enc.Descriptor)
			st.dists = append(st.dists, d)
			if 0 < d && d < certainty {
				st.matches++
				results = append(results, fmt.Sprintf("%.3f %s", shown(d), labels[owner[i]]))
			} else {
				results = append(results, fmt.Sprintf("%.3f no match", shown(d)))
			}
		}
		rec := c.Now().Sub(t)

		if len(rects) == 0 {
			results = []string{"no face"}
		} else {
			st.withFace++
			st.rec += rec
		}
		c.printf("%s  %5d  %6d  %s\n", line, len(rects), rec.Milliseconds(), strings.Join(results, ", "))

		if s.Exposure != -1 {
			// Some cameras only take manual exposure if it's set on every frame
			_ = cam.SetExposure(s.Exposure)
		}
	}
}

type testStats struct {
	frames, dark, withFace, matches int
	dists                           []float64
	rec                             time.Duration
}

func (st *testStats) report(c *cmd, took time.Duration, s *config.Settings) {
	c.printf("\n%d frames in %.1f s", st.frames, took.Seconds())
	if took > 0 {
		c.printf(", %.1f fps", float64(st.frames)/took.Seconds())
	}
	c.printf("\n%d dark or black, %d with a face\n", st.dark, st.withFace)
	if st.withFace > 0 {
		c.printf("Recognition: %d ms per frame with a face\n", (st.rec / time.Duration(st.withFace)).Milliseconds())
	}
	if len(st.dists) == 0 {
		return
	}
	d := append([]float64(nil), st.dists...)
	sort.Float64s(d)
	median := d[len(d)/2]
	if len(d)%2 == 0 {
		median = (d[len(d)/2-1] + d[len(d)/2]) / 2
	}
	// A median close to the threshold means most frames miss, and each
	// miss costs a frame of unlock time
	close, knob := false, "raising certainty"
	if s.Recognizer == "sface" {
		knob = "lowering match_threshold"
		c.printf("Similarity: best %.3f, median %.3f, %d of %d faces over %g\n",
			face.Cosine(d[0]), face.Cosine(median), st.matches, len(d), s.MatchThreshold)
		close = face.Cosine(median) <= s.MatchThreshold+(1-s.MatchThreshold)*0.1
	} else {
		c.printf("Certainty: best %.3f, median %.3f, %d of %d faces under %g\n",
			d[0]*10, median*10, st.matches, len(d), s.Certainty)
		close = median*10 >= s.Certainty*0.9
	}
	if close {
		c.say("Most faces are close to or over the threshold. Enrolling again in your usual position")
		c.say("usually helps more than " + knob)
	}
}
