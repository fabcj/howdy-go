package config

import (
	"errors"
	"fmt"
	"os"
)

// Recognizer names a face backend
type Recognizer string

const (
	Dlib  Recognizer = "dlib"  // Howdy's: HOG or CNN detector, ResNet encoder
	SFace Recognizer = "sface" // YuNet and SFace on ONNX Runtime
)

// Settings holds every value howdy-compare and the CLI read, with the
// fallbacks the Python code ends up using
type Settings struct {
	UseCNN bool

	// Recognizer is dlib (Howdy's) or sface (YuNet and SFace). MatchThreshold
	// is sface's least cosine similarity for a match, certainty is dlib's.
	Recognizer     Recognizer
	MatchThreshold float64

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
	rec, err := f.String("core", "recognizer", string(Dlib))
	s.Recognizer = Recognizer(rec)
	errs = append(errs, err)
	// SFace's own threshold in OpenCV Zoo (sface.py)
	fl(&s.MatchThreshold, "video", "match_threshold", 0.363)
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
	if s.Recognizer != Dlib && s.Recognizer != SFace {
		return nil, fmt.Errorf("[core] recognizer must be dlib or sface, not %q", s.Recognizer)
	}
	if !(s.MatchThreshold > 0 && s.MatchThreshold < 1) {
		return nil, fmt.Errorf("[video] match_threshold must be between 0 and 1, not %v", s.MatchThreshold)
	}
	return &s, nil
}
