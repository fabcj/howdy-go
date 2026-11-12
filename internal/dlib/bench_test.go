//go:build !nodlib

package dlib

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fabcj/howdy-go/internal/paths"
)

// Needs the installed dlib data and HOWDY_GO_TESTDATA (a public domain
// portrait from tools/fetch-test-images), since encoding needs a real face
func benchSetup(b *testing.B) (*Models, string) {
	dir := os.Getenv("HOWDY_GO_TESTDATA")
	if dir == "" {
		b.Skip("HOWDY_GO_TESTDATA not set")
	}
	l := paths.Installed()
	m, err := Load(l.HOGDetector(), l.CNNDetector(), l.ShapePredictor(), l.Recognizer(), false, 0)
	if err != nil {
		b.Skip(err)
	}
	b.Cleanup(m.Close)
	return m, filepath.Join(dir, "glenn.pgm")
}

func BenchmarkDetect(b *testing.B) {
	m, path := benchSetup(b)
	g, err := readPGM(path)
	if err != nil {
		b.Skip(err)
	}
	for b.Loop() {
		if _, err := m.Detect(g); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncode(b *testing.B) {
	m, path := benchSetup(b)
	g, err := readPGM(path)
	if err != nil {
		b.Skip(err)
	}
	rects, err := m.Detect(g)
	if err != nil || len(rects) == 0 {
		b.Skip("no face found in the test image")
	}
	for b.Loop() {
		if _, err := m.Encode(g, rects[0]); err != nil {
			b.Fatal(err)
		}
	}
}
