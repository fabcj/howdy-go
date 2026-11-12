// Package loader loads the face recognisers, which needs dlib and ONNX
// Runtime through cgo
package loader

import (
	"github.com/fabcj/howdy-go/internal/config"
	"github.com/fabcj/howdy-go/internal/dlib"
	"github.com/fabcj/howdy-go/internal/face"
	"github.com/fabcj/howdy-go/internal/paths"
	"github.com/fabcj/howdy-go/internal/sface"
)

// Load loads the named recogniser
func Load(l paths.Layout, s *config.Settings, name config.Recognizer) (face.Recognizer, error) {
	if name == config.SFace {
		m, err := sface.Load(l.ONNXDir)
		if err != nil {
			return nil, err
		}
		return m, nil
	}
	m, err := dlib.Load(l.HOGDetector(), l.CNNDetector(), l.ShapePredictor(), l.Recognizer(),
		s.UseCNN, s.DetectionUpsample)
	if err != nil {
		return nil, err
	}
	return m, nil
}
