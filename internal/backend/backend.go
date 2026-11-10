// Package backend is what compare and the CLI need to know about the face
// recognisers without loading one: the sface model files, how its stored
// descriptors are tagged, and which stored encodings go with the
// recogniser config.ini asks for. Loading lives in backend/loader, so
// this package needs no cgo.
package backend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fabcj/howdy-go/internal/config"
	"github.com/fabcj/howdy-go/internal/model"
	"github.com/fabcj/howdy-go/internal/paths"
)

// The sface model files and what they must hash to (OpenCV Zoo's git-LFS ids)
const (
	YuNetFile   = "face_detection_yunet_2026may.onnx"
	YuNetSHA256 = "ebafce4e3c118d6554634be5c27ab333b4c047a9a8c3faf1d7cf93101c22f0f0"
	SFaceFile   = "face_recognition_sface_2021dec.onnx"
	SFaceSHA256 = "0ba9fbfa01b5270c96627c4ef784da859931e02f04419c829e83484087c34e79"
)

// SFaceEncoder tags every stored sface descriptor, so they're never
// compared with another model's
const SFaceEncoder = "sface-2021dec"

// SFaceSize is how many numbers an sface descriptor has
const SFaceSize = 128

// MissingData says what to do when the named recogniser's model files
// aren't installed, or "" when they are
func MissingData(name string) string {
	if name == "sface" {
		for _, f := range []string{YuNetFile, SFaceFile} {
			if _, err := os.Stat(filepath.Join(paths.ONNXDir, f)); err != nil {
				return fmt.Sprintf("The sface models are missing from %s, reinstall howdy-go or set recognizer = dlib", paths.ONNXDir)
			}
		}
		return ""
	}
	if _, err := os.Stat(paths.ShapePredictor()); err != nil {
		return "Data files have not been downloaded, please run the following commands:\n\n\tcd " +
			paths.DlibDataDir + "\n\tsudo ./install.sh\n"
	}
	return ""
}

// ErrNotEnrolled means the user has dlib models but nothing for the
// configured recogniser
var ErrNotEnrolled = errors.New("no sface model, run howdy add again")

// Known is what compare matches against for the configured recogniser. dat
// is the user's .dat file, which decides which models exist; owner indexes
// its entries.
func Known(s *config.Settings, user string, dat *model.Models) (known [][]float64, owner []int, err error) {
	if s.Recognizer != "sface" {
		known, owner = dat.Flat()
		return known, owner, nil
	}
	m, err := model.LoadSize(paths.SFaceModelsDir, user, paths.ModelOwnerUID(), SFaceSize)
	if errors.Is(err, model.ErrNoModel) {
		return nil, nil, ErrNotEnrolled
	}
	if err != nil {
		return nil, nil, err
	}
	known, owner = m.Enrolled(dat, SFaceEncoder)
	if len(known) == 0 {
		return nil, nil, ErrNotEnrolled
	}
	return known, owner, nil
}
