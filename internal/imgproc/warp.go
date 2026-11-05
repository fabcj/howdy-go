package imgproc

import "math"

// WarpAffine is cv2.warpAffine(g, m, (w, h)) with INTER_LINEAR and a
// constant 0 border, as OpenCV 4.11 and later compute it: float32 source
// positions and float32 bilinear weights (warp_kernels.simd.hpp). m maps
// source to destination, as a 2x3 matrix by rows.
func WarpAffine(g *Gray, m [6]float64, w, h int) *Gray {
	// Inverted in double the way OpenCV does, then used as float32
	d := m[0]*m[4] - m[1]*m[3]
	if d != 0 {
		d = 1 / d
	}
	a11, a22 := m[4]*d, m[0]*d
	m[0], m[4] = a11, a22
	m[1] *= -d
	m[3] *= -d
	b1 := -m[0]*m[2] - m[1]*m[5]
	b2 := -m[3]*m[2] - m[4]*m[5]
	m[2], m[5] = b1, b2
	var f [6]float32
	for i, v := range m {
		f[i] = float32(v)
	}

	out := NewGray(w, h)
	px := func(x, y int) float32 {
		if x < 0 || y < 0 || x >= g.W || y >= g.H {
			return 0
		}
		return float32(g.Pix[y*g.W+x])
	}
	for y := range h {
		fy := float32(y)
		rx := f[1]*fy + f[2]
		ry := f[4]*fy + f[5]
		for x := range w {
			fx := float32(x)
			sx := fma32(f[0], fx, rx)
			sy := fma32(f[3], fx, ry)
			x0, y0 := floor32(sx), floor32(sy)
			alpha, beta := sx-float32(x0), sy-float32(y0)
			p00, p01 := px(x0, y0), px(x0+1, y0)
			p10, p11 := px(x0, y0+1), px(x0+1, y0+1)
			top := fma32(alpha, p01-p00, p00)
			bottom := fma32(alpha, p11-p10, p10)
			v := fma32(beta, bottom-top, top)
			out.Pix[y*w+x] = saturate(int(math.RoundToEven(float64(v))))
		}
	}
	return out
}

func fma32(a, b, c float32) float32 {
	return float32(math.FMA(float64(a), float64(b), float64(c)))
}

func floor32(v float32) int {
	return int(math.Floor(float64(v)))
}
