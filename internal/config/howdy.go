package config

import (
	"errors"
	"fmt"
	"os"
)

// Settings holds every value howdy-compare and the CLI read, with the
// fallbacks the Python code ends up using
type Settings struct {
	UseCNN bool

	Certainty         float64 // as written, compare divides it by 10
	RequiredMatches   int
	Timeout           int
	DevicePath        string
	WarnNoDevice      bool
	MaxHeight         float64
	FrameWidth        int
	FrameHeight       int
	DeviceFPS         int
	DarkThreshold     float64
	DetectionUpsample int
	EnrollFrames      int
	Exposure          int
	Rotate            int

	SaveFailed     bool
	SaveSuccessful bool

	Rubberstamps bool
	EndReport    bool
}

// Read parses the config file at path
func Read(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Load pulls the settings out of a parsed file
func Load(f *File) (*Settings, error) {
	var s Settings
	var errs []error
	b := func(dst *bool, sec, key string, fb bool) {
		v, err := f.Bool(sec, key, fb)
		*dst = v
		errs = append(errs, err)
	}
	i := func(dst *int, sec, key string, fb int) {
		v, err := f.Int(sec, key, fb)
		*dst = v
		errs = append(errs, err)
	}
	fl := func(dst *float64, sec, key string, fb float64) {
		v, err := f.Float(sec, key, fb)
		*dst = v
		errs = append(errs, err)
	}

	b(&s.UseCNN, "core", "use_cnn", false)
	fl(&s.Certainty, "video", "certainty", 3.5)
	i(&s.RequiredMatches, "video", "required_matches", 1)
	i(&s.Timeout, "video", "timeout", 4)
	b(&s.WarnNoDevice, "video", "warn_no_device", true)
	fl(&s.MaxHeight, "video", "max_height", 320)
	i(&s.FrameWidth, "video", "frame_width", -1)
	i(&s.FrameHeight, "video", "frame_height", -1)
	i(&s.DeviceFPS, "video", "device_fps", -1)
	// compare.py reads this twice, 50 first, but the loop uses the second read
	fl(&s.DarkThreshold, "video", "dark_threshold", 60)
	i(&s.DetectionUpsample, "video", "detection_upsample", 1)
	i(&s.EnrollFrames, "video", "enroll_frames", 15)
	i(&s.Exposure, "video", "exposure", -1)
	i(&s.Rotate, "video", "rotate", 0)
	b(&s.SaveFailed, "snapshots", "save_failed", false)
	b(&s.SaveSuccessful, "snapshots", "save_successful", false)
	b(&s.Rubberstamps, "rubberstamps", "enabled", false)
	b(&s.EndReport, "debug", "end_report", false)

	dev, err := f.Raw("video", "device_path")
	if errors.Is(err, ErrMissing) {
		err = errors.New("[video] device_path isn't set")
	}
	s.DevicePath = dev
	errs = append(errs, err)

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	s.RequiredMatches = max(1, s.RequiredMatches)
	s.EnrollFrames = max(1, s.EnrollFrames)
	if s.DetectionUpsample < 0 || s.DetectionUpsample > 4 {
		return nil, fmt.Errorf("[video] detection_upsample must be between 0 and 4, not %d", s.DetectionUpsample)
	}
	return &s, nil
}
