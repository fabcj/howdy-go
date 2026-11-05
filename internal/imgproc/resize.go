package imgproc

import "math"

// ScaledSize is the size cv2.resize(None, fx=f, fy=f) picks
func ScaledSize(w, h int, f float64) (int, int) {
	return int(math.RoundToEven(float64(w) * f)), int(math.RoundToEven(float64(h) * f))
}

type areaTap struct {
	di, si int
	alpha  float32
}

// OpenCV's computeResizeAreaTab
func areaTab(ssize, dsize int, scale float64) []areaTap {
	var tab []areaTap
	for dx := 0; dx < dsize; dx++ {
		fsx1 := float64(dx) * scale
		fsx2 := fsx1 + scale
		cell := math.Min(scale, float64(ssize)-fsx1)
		sx1 := int(math.Ceil(fsx1))
		sx2 := int(math.Floor(fsx2))
		sx2 = min(sx2, ssize-1)
		sx1 = min(sx1, sx2)
		if float64(sx1)-fsx1 > 1e-3 {
			tab = append(tab, areaTap{dx, sx1 - 1, float32((float64(sx1) - fsx1) / cell)})
		}
		for sx := sx1; sx < sx2; sx++ {
			tab = append(tab, areaTap{dx, sx, float32(1 / cell)})
		}
		if fsx2-float64(sx2) > 1e-3 {
			tab = append(tab, areaTap{dx, sx2, float32(math.Min(math.Min(fsx2-float64(sx2), 1), cell) / cell)})
		}
	}
	return tab
}

// ShrinkArea is cv2.resize(g, None, fx=f, fy=f, interpolation=INTER_AREA)
// for 0 < f < 1. Each output pixel is the average of the source area it
// covers, with partly covered pixels weighted by how much they overlap.
func ShrinkArea(g *Gray, f float64) *Gray {
	dw, dh := ScaledSize(g.W, g.H, f)
	dw, dh = max(dw, 1), max(dh, 1)
	// With fx and fy given, OpenCV scales by 1/f, not by the size ratio
	scaleX := 1 / f
	scaleY := 1 / f

	xtab := areaTab(g.W, dw, scaleX)
	ytab := areaTab(g.H, dh, scaleY)

	out := NewGray(dw, dh)
	if f == 0.5 {
		shrinkHalf(g, out)
		return out
	}
	buf := make([]float32, dw)
	sum := make([]float32, dw)
	prevDy := 0
	flush := func(dy int) {
		for x := range sum {
			out.Pix[dy*dw+x] = saturate(roundf(sum[x]))
			sum[x] = 0
		}
	}
	for i, yt := range ytab {
		if i > 0 && yt.di != prevDy {
			flush(prevDy)
		}
		prevDy = yt.di
		for x := range buf {
			buf[x] = 0
		}
		row := g.Pix[yt.si*g.W:]
		for _, xt := range xtab {
			buf[xt.di] += float32(row[xt.si]) * xt.alpha
		}
		for x := range sum {
			sum[x] += buf[x] * yt.alpha
		}
	}
	flush(prevDy)
	return out
}

// OpenCV's fast path for exactly half size rounds ties up: (a+b+c+d+2)>>2.
// Blocks cut off at the right or bottom edge average what's there.
func shrinkHalf(g, out *Gray) {
	for dy := 0; dy < out.H; dy++ {
		for dx := 0; dx < out.W; dx++ {
			sum, n := 0, 0
			for sy := 2 * dy; sy < min(2*dy+2, g.H); sy++ {
				for sx := 2 * dx; sx < min(2*dx+2, g.W); sx++ {
					sum += int(g.Pix[sy*g.W+sx])
					n++
				}
			}
			if n == 4 {
				out.Pix[dy*out.W+dx] = byte((sum + 2) >> 2)
			} else if n > 0 {
				out.Pix[dy*out.W+dx] = saturate(roundf(float32(sum) / float32(n)))
			}
		}
	}
}
