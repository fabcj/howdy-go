package sface

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"howdy-go/internal/backend"
	"howdy-go/internal/imgproc"
	"howdy-go/internal/onnx"
)

// Compares against OpenCV's FaceDetectorYN and FaceRecognizerSF. Set
// HOWDY_GO_TESTDATA to a directory that has the .pgm frames and the
// sface-reference.json from tools/sface-reference.py, and HOWDY_GO_MODELS
// to where tools/fetch-face-models put the models.
func TestMatchesOpenCV(t *testing.T) {
	dir, models := os.Getenv("HOWDY_GO_TESTDATA"), os.Getenv("HOWDY_GO_MODELS")
	if dir == "" || models == "" {
		t.Skip("HOWDY_GO_TESTDATA or HOWDY_GO_MODELS not set")
	}
	if !onnx.Available {
		t.Skip("no ONNX Runtime")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "sface-reference.json"))
	if err != nil {
		t.Skip(err)
	}
	var ref map[string]json.RawMessage
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	var score float32
	if err := json.Unmarshal(ref["score_threshold"], &score); err != nil {
		t.Fatal(err)
	}
	delete(ref, "score_threshold")

	det, err := onnx.Open(filepath.Join(models, "yunet_2026may.onnx"), backend.YuNetSHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer det.Close()
	rec, err := onnx.Open(filepath.Join(models, "sface_2021dec.onnx"), backend.SFaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	m := &Models{det: det, rec: rec, recOut: rec.Output0()}
	defer m.Close()

	var worstBox, worstScore, worstCos float64 = 0, 0, 1
	worstPix := 0
	for name, msg := range ref {
		var want []struct {
			Row     [15]float64
			Crop    []int
			Feature []float64
		}
		if err := json.Unmarshal(msg, &want); err != nil {
			t.Fatal(err)
		}
		g, err := readPGM(filepath.Join(dir, name+".pgm"))
		if err != nil {
			t.Fatal(err)
		}
		found, err := detect(det, imgproc.CLAHE(g, 2.0, 8, 8), score)
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != len(want) {
			t.Errorf("%s: %d faces, OpenCV %d", name, len(found), len(want))
			continue
		}
		for i, d := range found {
			w := want[i]
			got := []float32{d.X, d.Y, d.W, d.H}
			for _, p := range d.Points {
				got = append(got, p[0], p[1])
			}
			for j, v := range got {
				worstBox = math.Max(worstBox, math.Abs(float64(v)-w.Row[j]))
			}
			worstScore = math.Max(worstScore, math.Abs(float64(d.Score)-w.Row[14]))

			// The crop from OpenCV's own points, so this checks the warp and
			// the transform alone
			var pts [5][2]float32
			for n := range 5 {
				pts[n] = [2]float32{float32(w.Row[4+2*n]), float32(w.Row[5+2*n])}
			}
			crop := Align(g, pts)
			for j, v := range crop.Pix {
				worstPix = max(worstPix, abs(int(v)-w.Crop[j]))
			}
			// And the feature from OpenCV's crop, which checks SFace alone
			wantCrop := imgproc.NewGray(112, 112)
			for j, v := range w.Crop {
				wantCrop.Pix[j] = byte(v)
			}
			feat, err := m.feature(wantCrop)
			if err != nil {
				t.Fatal(err)
			}
			worstCos = math.Min(worstCos, cosine(normalise(feat), w.Feature))
		}
	}
	t.Logf("boxes and points within %.4f px, scores within %.2g, crops within %d, features cosine >= %.6f",
		worstBox, worstScore, worstPix, worstCos)
	if worstBox > 0.05 || worstScore > 1e-3 {
		t.Errorf("detections differ from OpenCV: %.4f px, score %.2g", worstBox, worstScore)
	}
	if worstPix > 1 {
		t.Errorf("aligned crops differ from OpenCV by up to %d", worstPix)
	}
	if worstCos < 0.9999 {
		t.Errorf("features differ from OpenCV: cosine %.6f", worstCos)
	}
}

func cosine(a, b []float64) float64 {
	var ab, aa, bb float64
	for i := range a {
		ab += a[i] * b[i]
		aa += a[i] * a[i]
		bb += b[i] * b[i]
	}
	return ab / math.Sqrt(aa*bb)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func readPGM(path string) (*imgproc.Gray, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fields := bytes.Fields(data[:min(len(data), 64)])
	if len(fields) < 4 || string(fields[0]) != "P5" {
		return nil, fmt.Errorf("%s isn't a P5 pgm", path)
	}
	w, _ := strconv.Atoi(string(fields[1]))
	h, _ := strconv.Atoi(string(fields[2]))
	pix := data[len(data)-w*h:]
	return &imgproc.Gray{W: w, H: h, Pix: append([]byte(nil), pix...)}, nil
}
