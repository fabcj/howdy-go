// Package imgproc has the few image operations compare needs, written to
// give the same results as the OpenCV calls Howdy's Python code makes.
package imgproc

import "math"

// Gray is an 8-bit single channel image, rows packed with no padding
type Gray struct {
	W, H int
	Pix  []byte
}

// NewGray allocates a black image
func NewGray(w, h int) *Gray {
	return &Gray{W: w, H: h, Pix: make([]byte, w*h)}
}

// At returns the pixel at x, y
func (g *Gray) At(x, y int) byte { return g.Pix[y*g.W+x] }

// roundf is cvRound on a float: nearest, ties to even
func roundf(v float32) int {
	return int(math.RoundToEven(float64(v)))
}

func saturate(v int) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

// Darkness is compare.py's 8 bin histogram test: the share of pixels below
// 32, as a percentage. The maths is float32 like numpy's on calcHist output.
// total is 0 only for an empty image.
func Darkness(g *Gray) (darkness float32, total int) {
	dark := 0
	for _, v := range g.Pix {
		if v < 32 {
			dark++
		}
	}
	total = len(g.Pix)
	if total == 0 {
		return 0, 0
	}
	return float32(dark) / float32(total) * 100, total
}

// RotateCCW is cv2.rotate with ROTATE_90_COUNTERCLOCKWISE
func RotateCCW(g *Gray) *Gray {
	out := NewGray(g.H, g.W)
	for y := 0; y < out.H; y++ {
		for x := 0; x < out.W; x++ {
			out.Pix[y*out.W+x] = g.Pix[x*g.W+(g.W-1-y)]
		}
	}
	return out
}

// RotateCW is cv2.rotate with ROTATE_90_CLOCKWISE
func RotateCW(g *Gray) *Gray {
	out := NewGray(g.H, g.W)
	for y := 0; y < out.H; y++ {
		for x := 0; x < out.W; x++ {
			out.Pix[y*out.W+x] = g.Pix[(g.H-1-x)*g.W+y]
		}
	}
	return out
}
