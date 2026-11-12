// Package sface is the newer face backend: YuNet finds faces and their five
// landmarks, SFace turns each aligned face into 128 numbers. Both are
// OpenCV Zoo models (MIT and Apache-2.0) run with ONNX Runtime, and both
// steps are ports of OpenCV's FaceDetectorYN and FaceRecognizerSF, checked
// against them by the reference test. Descriptors are L2 normalised, so
// matching by Euclidean distance is matching by cosine (face.CosineDistance).
package sface

import (
	"errors"
	"math"
	"path/filepath"

	"github.com/fabcj/howdy-go/internal/backend"
	"github.com/fabcj/howdy-go/internal/face"
	"github.com/fabcj/howdy-go/internal/imgproc"
	"github.com/fabcj/howdy-go/internal/onnx"
)

// ScoreThreshold is the least YuNet score that counts as a face. Real faces
// on an IR camera score 0.90 to 0.94, a phone held up with a photo 0.62 to
// 0.66 (tools/face-eval.py)
const ScoreThreshold = 0.85

// Models holds the two loaded models and the buffers one frame needs,
// kept between frames: a YuNet blob is near 3 MB, and Detect and Encode
// are called from one goroutine at a time
type Models struct {
	det, rec       *onnx.Session
	detOut, recOut *onnx.Outputs
	detCaps        []int
	blob, res      []float32
}

// Load loads YuNet and SFace from dir, checking their hashes first
func Load(dir string) (*Models, error) {
	det, err := onnx.Open(filepath.Join(dir, backend.YuNetFile), backend.YuNetSHA256)
	if err != nil {
		return nil, err
	}
	rec, err := onnx.Open(filepath.Join(dir, backend.SFaceFile), backend.SFaceSHA256)
	if err != nil {
		det.Close()
		return nil, err
	}
	m := &Models{det: det, rec: rec}
	if m.recOut, err = rec.Outputs([]string{rec.Output0()}, []int{backend.SFaceSize}); err != nil {
		m.Close()
		return nil, err
	}
	return m, nil
}

// Close frees the models
func (m *Models) Close() {
	m.det.Close()
	m.rec.Close()
	if m.detOut != nil {
		m.detOut.Close()
	}
	if m.recOut != nil {
		m.recOut.Close()
	}
}

// Detect finds faces in a grey image, best score first
func (m *Models) Detect(g *imgproc.Gray) ([]face.Rect, error) {
	found, err := m.detect(g, ScoreThreshold)
	if err != nil {
		return nil, err
	}
	out := make([]face.Rect, len(found))
	for i, d := range found {
		out[i] = face.Rect{
			Left: roundInt(d.X), Top: roundInt(d.Y),
			Right: roundInt(d.X+d.W) - 1, Bottom: roundInt(d.Y+d.H) - 1,
			Landmarks: d.Points, HasLandmarks: true,
		}
	}
	return out, nil
}

// Encode aligns the face onto SFace's template and returns its descriptor,
// L2 normalised
func (m *Models) Encode(g *imgproc.Gray, r face.Rect) (face.Encoding, error) {
	var enc face.Encoding
	if !r.HasLandmarks {
		return enc, errors.New("face box has no landmarks")
	}
	if len(g.Pix) == 0 || len(g.Pix) != g.W*g.H {
		return enc, errors.New("empty image")
	}
	feat, err := m.feature(Align(g, r.Landmarks))
	if err != nil {
		return enc, err
	}
	enc.Descriptor = normalise(feat)
	for i, p := range r.Landmarks {
		enc.Parts[i] = [2]int{roundInt(p[0]), roundInt(p[1])}
	}
	return enc, nil
}

// Align is FaceRecognizerSF::alignCrop: the face warped to 112x112
func Align(g *imgproc.Gray, points [5][2]float32) *imgproc.Gray {
	return imgproc.WarpAffine(g, similarity(points), 112, 112)
}

// feature is FaceRecognizerSF::feature: the crop as 0-255 floats, the same
// in all three channels, so swapping red and blue changes nothing
func (m *Models) feature(crop *imgproc.Gray) ([]float32, error) {
	const plane = 112 * 112
	if cap(m.blob) < 3*plane {
		m.blob = make([]float32, 3*plane)
	}
	blob := m.blob[:3*plane]
	for i, v := range crop.Pix {
		blob[i] = float32(v)
	}
	copy(blob[plane:], blob[:plane])
	copy(blob[2*plane:], blob[:plane])
	if cap(m.res) < m.recOut.Total {
		m.res = make([]float32, m.recOut.Total)
	}
	out, err := m.rec.Run(blob, []int64{1, 3, 112, 112}, m.recOut, m.res[:m.recOut.Total])
	if err != nil {
		return nil, err
	}
	if len(out[0]) != backend.SFaceSize {
		return nil, errors.New("SFace gave a descriptor of the wrong size")
	}
	return out[0], nil
}

func normalise(v []float32) []float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	n := math.Sqrt(sum)
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = float64(x) / n
	}
	return out
}

func roundInt(v float32) int { return int(math.Round(float64(v))) }
