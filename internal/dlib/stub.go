//go:build nodlib

// Package dlib is built without dlib here, for working on everything else.
// Release builds never use the nodlib tag.
package dlib

import (
	"errors"

	"howdy-go/internal/face"
	"howdy-go/internal/imgproc"
)

// Available is false in this build
const Available = false

var errNoDlib = errors.New("built without dlib (nodlib tag)")

type Models struct{}

func Load(hogPath, cnnPath, shapePath, encoderPath string, useCNN bool, upsample int) (*Models, error) {
	return nil, errNoDlib
}

func (m *Models) Close() {}

func (m *Models) Detect(*imgproc.Gray) ([]face.Rect, error) { return nil, errNoDlib }

func (m *Models) DetectWith(*imgproc.Gray, int, bool) ([]face.Rect, error) { return nil, errNoDlib }

func (m *Models) Encode(*imgproc.Gray, face.Rect) (face.Encoding, error) {
	return face.Encoding{}, errNoDlib
}

func SaveFrontalDetector(string) error { return errNoDlib }
