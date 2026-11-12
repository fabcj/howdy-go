package sface

import (
	"testing"

	"github.com/fabcj/howdy-go/internal/face"
	"github.com/fabcj/howdy-go/internal/imgproc"
	"github.com/fabcj/howdy-go/internal/paths"
)

// Needs the installed models. On a frame with no face the detector does the
// same work, and Encode gets a made-up face.
func benchModels(b *testing.B) *Models {
	m, err := Load(paths.Installed().ONNXDir)
	if err != nil {
		b.Skip(err)
	}
	b.Cleanup(m.Close)
	return m
}

func benchFrame(w, h int) *imgproc.Gray {
	g := imgproc.NewGray(w, h)
	for i := range g.Pix {
		g.Pix[i] = byte(40 + (i*7+i/w*3)%180)
	}
	return g
}

func BenchmarkDetect(b *testing.B) {
	m := benchModels(b)
	g := benchFrame(640, 360)
	for b.Loop() {
		if _, err := m.Detect(g); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncode(b *testing.B) {
	m := benchModels(b)
	g := benchFrame(640, 360)
	r := face.Rect{HasLandmarks: true, Landmarks: [5][2]float32{{250, 150}, {350, 150}, {300, 200}, {260, 250}, {340, 250}}}
	for b.Loop() {
		if _, err := m.Encode(g, r); err != nil {
			b.Fatal(err)
		}
	}
}
