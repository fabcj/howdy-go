//go:build !nodlib

package dlib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/fabcj/howdy-go/internal/imgproc"
	"github.com/fabcj/howdy-go/internal/paths"
)

// Compares against python-dlib. Set HOWDY_GO_TESTDATA to a directory
// filled by tools/fetch-test-images and tools/dlib-reference.py.
func TestParity(t *testing.T) {
	dir := os.Getenv("HOWDY_GO_TESTDATA")
	if dir == "" {
		t.Skip("HOWDY_GO_TESTDATA not set")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "reference.json"))
	if err != nil {
		t.Skip(err)
	}
	type refFace struct {
		Box      [4]int
		Parts    [5][2]int
		Encoding []float64
	}
	var ref map[string]map[string][]refFace
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}

	for _, cnn := range []bool{false, true} {
		l := paths.Installed()
		m, err := Load(l.HOGDetector(), l.CNNDetector(), l.ShapePredictor(), l.Recognizer(), cnn, 0)
		if err != nil {
			t.Fatal(err)
		}
		worst := 0.0
		for name, runs := range ref {
			g, err := readPGM(filepath.Join(dir, name+".pgm"))
			if err != nil {
				t.Fatal(err)
			}
			gs := imgproc.CLAHE(g, 2.0, 8, 8)
			for _, up := range []int{0, 1} {
				key := fmt.Sprintf("hog%d", up)
				if cnn {
					if up != 0 {
						continue
					}
					key = "cnn0"
				}
				want := runs[key]
				rects, err := m.DetectWith(gs, up, cnn)
				if err != nil {
					t.Fatal(err)
				}
				if len(rects) != len(want) {
					t.Errorf("%s %s: %d faces, python-dlib %d", name, key, len(rects), len(want))
					continue
				}
				for i, r := range rects {
					w := want[i]
					if [4]int{r.Left, r.Top, r.Right, r.Bottom} != w.Box {
						t.Errorf("%s %s face %d: box %v, python-dlib %v", name, key, i, r, w.Box)
						continue
					}
					enc, err := m.Encode(g, r)
					if err != nil {
						t.Fatal(err)
					}
					if enc.Parts != w.Parts {
						t.Errorf("%s %s face %d: landmarks %v, python-dlib %v", name, key, i, enc.Parts, w.Parts)
					}
					for j, v := range w.Encoding {
						worst = math.Max(worst, math.Abs(enc.Descriptor[j]-v))
					}
				}
			}
		}
		m.Close()
		t.Logf("cnn=%v: largest encoding difference %g", cnn, worst)
		if worst > 1e-5 {
			t.Errorf("cnn=%v: encodings differ by up to %g", cnn, worst)
		}
	}
}

func readPGM(path string) (*imgproc.Gray, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var w, h, maxv int
	r := bytes.NewReader(data)
	if _, err := fmt.Fscanf(r, "P5\n%d %d\n%d\n", &w, &h, &maxv); err != nil || maxv != 255 {
		return nil, fmt.Errorf("%s: not an 8 bit PGM", path)
	}
	pix := data[len(data)-r.Len():]
	if len(pix) != w*h {
		return nil, fmt.Errorf("%s: %d bytes of pixels for %dx%d", path, len(pix), w, h)
	}
	g := imgproc.NewGray(w, h)
	copy(g.Pix, pix)
	return g, nil
}
