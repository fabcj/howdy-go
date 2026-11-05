package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"howdy-go/internal/face"
	"howdy-go/internal/imgproc"
	"howdy-go/internal/model"
	"howdy-go/internal/paths"
)

// test is Howdy's cli/test.py as a terminal readout instead of a window,
// which can't open under sudo on Wayland. One line per frame, then a
// summary on ctrl+C to tune certainty with.
func (c *cmd) test() int {
	if _, err := os.Stat(paths.ShapePredictor()); err != nil {
		c.say("Data files have not been downloaded, please run the following commands:")
		c.say("\n\tcd " + paths.DlibDataDir)
		c.say("\tsudo ./install.sh\n")
		return 1
	}
	_, s, err := c.settings()
	if err != nil {
		return c.errorf("%v", err)
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
		known, owner = m.Flat()
		for _, e := range m.Entries {
			labels = append(labels, model.Label(e))
		}
	}
	certainty := s.Certainty / 10

	faces, err := c.Faces(s)
	if err != nil {
		return c.errorf("can't load the dlib models: %v", err)
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
	c.printf("Certainty %g, dark threshold %g, upsample %d\n", s.Certainty, s.DarkThreshold, s.DetectionUpsample)
	c.say("Press ctrl+C to stop\n")
	c.say("frame      ms  dark%  faces  rec ms  result")

	var st testStats
	start := c.Now()
	for {
		select {
		case <-stop:
			st.report(c, c.Now().Sub(start), s.Certainty)
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
		rects, err := faces.Detect(gs, s.DetectionUpsample, s.UseCNN)
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
			d, i := face.Best(known, &enc.Descriptor)
			st.dists = append(st.dists, d)
			if 0 < d && d < certainty {
				st.matches++
				results = append(results, fmt.Sprintf("%.3f %s", d*10, labels[owner[i]]))
			} else {
				results = append(results, fmt.Sprintf("%.3f no match", d*10))
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

// certainty is as written in the config, not divided by 10
func (st *testStats) report(c *cmd, took time.Duration, certainty float64) {
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
	c.printf("Certainty: best %.3f, median %.3f, %d of %d faces under %g\n",
		d[0]*10, median*10, st.matches, len(d), certainty)
	// A median close to the threshold means most frames miss, and each
	// miss costs a frame of unlock time
	if median*10 >= certainty*0.9 {
		c.say("Most faces are close to or over the threshold. Enrolling again in your usual position")
		c.say("usually helps more than raising certainty")
	}
}
