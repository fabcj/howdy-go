// Package backend is what compare and the CLI need to know about the face
// recognisers without loading one: whether their files are installed,
// which stored encodings go with the recogniser config.ini asks for, and
// how a distance is judged and shown. Loading lives in backend/loader, so
// this package needs no cgo.
package backend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fabcj/howdy-go/internal/config"
	"github.com/fabcj/howdy-go/internal/face"
	"github.com/fabcj/howdy-go/internal/model"
	"github.com/fabcj/howdy-go/internal/paths"
	"github.com/fabcj/howdy-go/internal/sface/spec"
)

// NoDeviceText is printed when device_path doesn't exist, by compare and
// the CLI alike
const NoDeviceText = "Howdy could not find a camera device at the path specified in the config file.\n" +
	"It is very likely that the path is not configured correctly, please edit the 'device_path' config value by running:\n" +
	"\n\tsudo howdy config\n"

// MissingData says what to do when the named recogniser's model files
// aren't installed, or "" when they are
func MissingData(l paths.Layout, name config.Recognizer) string {
	if name == config.SFace {
		for _, f := range []string{spec.YuNetFile, spec.SFaceFile} {
			if _, err := os.Stat(filepath.Join(l.ONNXDir, f)); err != nil {
				return fmt.Sprintf("The sface models are missing from %s, reinstall howdy-go or set recognizer = dlib", l.ONNXDir)
			}
		}
		return ""
	}
	if _, err := os.Stat(l.ShapePredictor()); err != nil {
		return "Data files have not been downloaded, please run the following commands:\n\n\tcd " +
			l.DlibDataDir + "\n\tsudo ./install.sh\n"
	}
	return ""
}

// MatcherFor is how compare judges and prints a distance with the
// configured recogniser: certainty for dlib, and for sface the distance
// between unit vectors at match_threshold's cosine similarity
func MatcherFor(s *config.Settings) face.Matcher {
	if s.Recognizer == config.SFace {
		return face.Matcher{
			Threshold: face.CosineDistance(s.MatchThreshold),
			Unit:      "Similarity",
			Shown:     face.Cosine,
			NoFace:    "none",
			Digits:    2,
		}
	}
	return face.Matcher{
		Threshold: s.Certainty / 10,
		Unit:      "Certainty",
		Shown:     func(d float64) float64 { return d * 10 },
		NoFace:    "100",
		Digits:    1,
	}
}

// ErrNotEnrolled means the user has dlib models but nothing for the
// configured recogniser
var ErrNotEnrolled = errors.New("no sface model, run howdy add again")

// Known is what compare matches against for the configured recogniser. dat
// is the user's .dat file, which decides which models exist; owner indexes
// its entries.
func Known(l paths.Layout, s *config.Settings, user string, dat *model.Models) (known [][]float64, owner []int, err error) {
	if s.Recognizer != config.SFace {
		known, owner = dat.Flat()
		return known, owner, nil
	}
	m, err := model.LoadSize(l.SFaceModelsDir, user, l.ModelOwner, spec.Size)
	if errors.Is(err, model.ErrNoModel) {
		return nil, nil, ErrNotEnrolled
	}
	if err != nil {
		return nil, nil, err
	}
	known, owner = m.Enrolled(dat, spec.Encoder)
	if len(known) == 0 {
		return nil, nil, ErrNotEnrolled
	}
	return known, owner, nil
}
