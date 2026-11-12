// Package paths holds the install locations. The PKGBUILD sets the
// defaults with -ldflags "-X github.com/fabcj/howdy-go/internal/paths.configDir=..."
// and so on (see the Makefile); Installed collects them into a Layout,
// which is passed to whatever needs a path. Tests build their own Layout.
package paths

import (
	"path/filepath"
	"strconv"
)

// Set at link time
var (
	configDir     = "/etc/howdy"
	userModelsDir = "/etc/howdy/models"
	dlibDataDir   = "/usr/share/dlib-data"
	logPath       = "/var/log/howdy"
	dataDir       = "/usr/share/howdy"

	// The sface backend: its ONNX models, and its descriptors, one file per
	// user like the models dir. Not inside the models dir, where howdy-gtk
	// takes every file for a user
	onnxDir        = "/usr/share/howdy-go/onnx"
	sfaceModelsDir = "/etc/howdy/models-sface"

	// The PIN hashes asked for after a face match, one root-only file per
	// user, in a directory only root can enter
	pinDir = "/etc/howdy/pin"
	// The PINs for PAM that runs as the user, such as lock screens: files
	// owned by the user in a directory owned by root
	pinUserDir = "/etc/howdy/pin-user"

	// Who must own the model files
	modelOwner = "0"
)

// Layout is where everything lives on one system
type Layout struct {
	ConfigDir      string
	UserModelsDir  string
	SFaceModelsDir string
	PinDir         string
	PinUserDir     string
	DlibDataDir    string
	ONNXDir        string
	DataDir        string
	LogPath        string
	ModelOwner     int // uid that must own the model files
}

// Installed is the layout this binary was built for
func Installed() Layout {
	owner, err := strconv.Atoi(modelOwner)
	if err != nil {
		owner = 0
	}
	return Layout{
		ConfigDir:      configDir,
		UserModelsDir:  userModelsDir,
		SFaceModelsDir: sfaceModelsDir,
		PinDir:         pinDir,
		PinUserDir:     pinUserDir,
		DlibDataDir:    dlibDataDir,
		ONNXDir:        onnxDir,
		DataDir:        dataDir,
		LogPath:        logPath,
		ModelOwner:     owner,
	}
}

func (l Layout) ConfigFile() string   { return filepath.Join(l.ConfigDir, "config.ini") }
func (l Layout) SnapshotsDir() string { return filepath.Join(l.LogPath, "snapshots") }
func (l Layout) Logo() string         { return filepath.Join(l.DataDir, "logo.png") }

func (l Layout) ShapePredictor() string {
	return filepath.Join(l.DlibDataDir, "shape_predictor_5_face_landmarks.dat")
}
func (l Layout) CNNDetector() string {
	return filepath.Join(l.DlibDataDir, "mmod_human_face_detector.dat")
}
func (l Layout) Recognizer() string {
	return filepath.Join(l.DlibDataDir, "dlib_face_recognition_resnet_model_v1.dat")
}
func (l Layout) HOGDetector() string {
	return filepath.Join(l.DlibDataDir, "frontal_face_detector.svm")
}
