// Package loader loads the face recognisers, which needs dlib and ONNX
// Runtime through cgo
package loader

import (
	"howdy-go/internal/config"
	"howdy-go/internal/dlib"
	"howdy-go/internal/face"
	"howdy-go/internal/paths"
	"howdy-go/internal/sface"
)

// Load loads the named recogniser: "dlib" or "sface"
func Load(s *config.Settings, name string) (face.Recognizer, error) {
	if name == "sface" {
		m, err := sface.Load(paths.ONNXDir)
		if err != nil {
			return nil, err
		}
		return m, nil
	}
	m, err := dlib.Load(paths.HOGDetector(), paths.CNNDetector(), paths.ShapePredictor(), paths.Recognizer(),
		s.UseCNN, s.DetectionUpsample)
	if err != nil {
		return nil, err
	}
	return m, nil
}
