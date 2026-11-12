package sface

import (
	"cmp"
	"errors"
	"math"
	"slices"

	"github.com/fabcj/howdy-go/internal/imgproc"
)

// Detection is one row of cv::FaceDetectorYN's output
type Detection struct {
	X, Y, W, H float32
	Points     [5][2]float32
	Score      float32
}

var (
	strides     = [3]int{8, 16, 32}
	yunetOutput = []string{"cls_8", "cls_16", "cls_32", "obj_8", "obj_16", "obj_32",
		"bbox_8", "bbox_16", "bbox_32", "kps_8", "kps_16", "kps_32"}
)

const (
	nmsThreshold = 0.3
	topK         = 5000
)

// detect is FaceDetectorYN::detect (objdetect/src/face_detect.cpp) on a
// grey image used as all three channels
func (m *Models) detect(g *imgproc.Gray, score float32) ([]Detection, error) {
	if len(g.Pix) == 0 || len(g.Pix) != g.W*g.H {
		return nil, errors.New("empty image")
	}
	// Padded with zeros on the right and bottom to a multiple of 32
	padW := ((g.W-1)/32 + 1) * 32
	padH := ((g.H-1)/32 + 1) * 32
	plane := padW * padH
	if cap(m.blob) < 3*plane {
		m.blob = make([]float32, 3*plane)
	}
	blob := m.blob[:3*plane]
	clear(blob[:plane])
	for y := 0; y < g.H; y++ {
		row := g.Pix[y*g.W : (y+1)*g.W]
		for x, v := range row {
			blob[y*padW+x] = float32(v)
		}
	}
	copy(blob[plane:], blob[:plane])
	copy(blob[2*plane:], blob[:plane])

	caps := make([]int, len(yunetOutput))
	for i, st := range strides {
		n := (padW / st) * (padH / st)
		caps[i], caps[i+3], caps[i+6], caps[i+9] = n, n, 4*n, 10*n
	}
	// The caps only change with the frame size, which they don't in a run
	if m.detOut == nil || !slices.Equal(caps, m.detCaps) {
		if m.detOut != nil {
			m.detOut.Close()
		}
		o, err := m.det.Outputs(yunetOutput, caps)
		if err != nil {
			return nil, err
		}
		m.detOut, m.detCaps = o, caps
	}
	if cap(m.res) < m.detOut.Total {
		m.res = make([]float32, m.detOut.Total)
	}
	outs, err := m.det.Run(blob, []int64{1, 3, int64(padH), int64(padW)}, m.detOut, m.res[:m.detOut.Total])
	if err != nil {
		return nil, err
	}
	for i, o := range outs {
		if len(o) != caps[i] {
			return nil, errors.New("YuNet gave outputs of the wrong size")
		}
	}

	var found []Detection
	for i, st := range strides {
		cols, rows := padW/st, padH/st
		cls, obj, bbox, kps := outs[i], outs[i+3], outs[i+6], outs[i+9]
		fs := float32(st)
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				idx := r*cols + c
				sc := sqrt32(clamp01(cls[idx]) * clamp01(obj[idx]))
				if sc < score {
					continue
				}
				cx := (float32(c) + bbox[idx*4]) * fs
				cy := (float32(r) + bbox[idx*4+1]) * fs
				w := exp32(bbox[idx*4+2]) * fs
				h := exp32(bbox[idx*4+3]) * fs
				d := Detection{X: cx - w/2, Y: cy - h/2, W: w, H: h, Score: sc}
				for n := range 5 {
					d.Points[n][0] = (kps[idx*10+2*n] + float32(c)) * fs
					d.Points[n][1] = (kps[idx*10+2*n+1] + float32(r)) * fs
				}
				found = append(found, d)
			}
		}
	}
	if len(found) <= 1 {
		return found, nil
	}
	return nms(found, score), nil
}

type irect struct{ x, y, w, h int }

func (a irect) overlap(b irect) float32 {
	x1, y1 := max(a.x, b.x), max(a.y, b.y)
	x2, y2 := min(a.x+a.w, b.x+b.w), min(a.y+a.h, b.y+b.h)
	inter := 0
	if x2 > x1 && y2 > y1 {
		inter = (x2 - x1) * (y2 - y1)
	}
	union := a.w*a.h + b.w*b.h - inter
	if union <= 0 {
		return 0
	}
	return float32(float64(inter) / float64(union))
}

// nms is cv::dnn::NMSBoxes on the boxes truncated to ints, as
// FaceDetectorYN calls it, with eta 1. The result is best score first.
func nms(found []Detection, score float32) []Detection {
	order := make([]int, 0, len(found))
	for i, d := range found {
		if d.Score > score {
			order = append(order, i)
		}
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(found[b].Score, found[a].Score) })
	if len(order) > topK {
		order = order[:topK]
	}
	box := func(d Detection) irect { return irect{int(d.X), int(d.Y), int(d.W), int(d.H)} }
	var keep []int
	for _, i := range order {
		ok := true
		for _, k := range keep {
			if box(found[i]).overlap(box(found[k])) > nmsThreshold {
				ok = false
				break
			}
		}
		if ok {
			keep = append(keep, i)
		}
	}
	out := make([]Detection, len(keep))
	for i, k := range keep {
		out[i] = found[k]
	}
	return out
}

func clamp01(v float32) float32 { return min(max(v, 0), 1) }

func sqrt32(v float32) float32 { return float32(math.Sqrt(float64(v))) }

func exp32(v float32) float32 { return float32(math.Exp(float64(v))) }
