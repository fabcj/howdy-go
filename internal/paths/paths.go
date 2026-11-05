// Package paths holds the install locations. The PKGBUILD sets them with
// -ldflags "-X howdy-go/internal/paths.ConfigDir=..." and so on.
package paths

import (
	"path/filepath"
	"strconv"
)

var (
	ConfigDir     = "/etc/howdy"
	UserModelsDir = "/etc/howdy/models"
	DlibDataDir   = "/usr/share/dlib-data"
	LogPath       = "/var/log/howdy"
	DataDir       = "/usr/share/howdy"

	// The sface backend: its ONNX models, and its descriptors, one file per
	// user like the models dir. Not inside the models dir, where howdy-gtk
	// takes every file for a user
	ONNXDir        = "/usr/share/howdy-go/onnx"
	SFaceModelsDir = "/etc/howdy/models-sface"

	// Who must own the model files. Only test builds change it
	ModelOwner = "0"
)

func ConfigFile() string   { return filepath.Join(ConfigDir, "config.ini") }
func SnapshotsDir() string { return filepath.Join(LogPath, "snapshots") }
func Logo() string         { return filepath.Join(DataDir, "logo.png") }

func ShapePredictor() string {
	return filepath.Join(DlibDataDir, "shape_predictor_5_face_landmarks.dat")
}
func CNNDetector() string { return filepath.Join(DlibDataDir, "mmod_human_face_detector.dat") }
func Recognizer() string {
	return filepath.Join(DlibDataDir, "dlib_face_recognition_resnet_model_v1.dat")
}
func HOGDetector() string { return filepath.Join(DlibDataDir, "frontal_face_detector.svm") }

// ModelOwnerUID is ModelOwner as a number
func ModelOwnerUID() int {
	n, err := strconv.Atoi(ModelOwner)
	if err != nil {
		return 0
	}
	return n
}
