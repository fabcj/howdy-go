package imgproc

import "math"

// CLAHE equalises g like cv2.createCLAHE(clipLimit, (tilesX, tilesY)).apply
// does for 8-bit images: a clipped histogram per tile, turned into a lookup
// table, and each pixel blended between the four nearest tile tables
func CLAHE(g *Gray, clipLimit float64, tilesX, tilesY int) *Gray {
	const histSize = 256

	// The tables are built on a copy padded to a whole number of tiles
	src := g
	if g.W%tilesX != 0 || g.H%tilesY != 0 {
		src = padReflect101(g, tilesX-g.W%tilesX, tilesY-g.H%tilesY, tilesX, tilesY)
	}
	tileW := src.W / tilesX
	tileH := src.H / tilesY
	tileArea := tileW * tileH

	clip := 0
	if clipLimit > 0 {
		clip = max(int(clipLimit*float64(tileArea)/histSize), 1)
	}
	lutScale := float32(histSize-1) / float32(tileArea)

	luts := make([]byte, tilesX*tilesY*histSize)
	var hist [histSize]int
	for ty := 0; ty < tilesY; ty++ {
		for tx := 0; tx < tilesX; tx++ {
			hist = [histSize]int{}
			for y := ty * tileH; y < (ty+1)*tileH; y++ {
				row := src.Pix[y*src.W+tx*tileW : y*src.W+(tx+1)*tileW]
				for _, v := range row {
					hist[v]++
				}
			}

			if clip > 0 {
				clipped := 0
				for i := range hist {
					if hist[i] > clip {
						clipped += hist[i] - clip
						hist[i] = clip
					}
				}
				batch := clipped / histSize
				residual := clipped - batch*histSize
				for i := range hist {
					hist[i] += batch
				}
				if residual != 0 {
					step := max(histSize/residual, 1)
					for i := 0; i < histSize && residual > 0; i, residual = i+step, residual-1 {
						hist[i]++
					}
				}
			}

			lut := luts[(ty*tilesX+tx)*histSize:]
			sum := 0
			for i := range hist {
				sum += hist[i]
				lut[i] = saturate(roundf(float32(sum) * lutScale))
			}
		}
	}

	// Blend on the original image, with tile centres at half a tile in
	invTW := 1 / float32(tileW)
	invTH := 1 / float32(tileH)
	ind1 := make([]int, g.W)
	ind2 := make([]int, g.W)
	xa := make([]float32, g.W)
	xa1 := make([]float32, g.W)
	for x := 0; x < g.W; x++ {
		txf := float32(x)*invTW - 0.5
		tx1 := int(math.Floor(float64(txf)))
		tx2 := tx1 + 1
		xa[x] = txf - float32(tx1)
		xa1[x] = 1 - xa[x]
		tx1 = max(tx1, 0)
		tx2 = min(tx2, tilesX-1)
		ind1[x] = tx1 * histSize
		ind2[x] = tx2 * histSize
	}

	out := NewGray(g.W, g.H)
	for y := 0; y < g.H; y++ {
		tyf := float32(y)*invTH - 0.5
		ty1 := int(math.Floor(float64(tyf)))
		ty2 := ty1 + 1
		ya := tyf - float32(ty1)
		ya1 := 1 - ya
		ty1 = max(ty1, 0)
		ty2 = min(ty2, tilesY-1)
		plane1 := luts[ty1*tilesX*histSize:]
		plane2 := luts[ty2*tilesX*histSize:]

		srcRow := g.Pix[y*g.W : (y+1)*g.W]
		dstRow := out.Pix[y*g.W : (y+1)*g.W]
		for x, v := range srcRow {
			i1 := ind1[x] + int(v)
			i2 := ind2[x] + int(v)
			res := (float32(plane1[i1])*xa1[x]+float32(plane1[i2])*xa[x])*ya1 +
				(float32(plane2[i1])*xa1[x]+float32(plane2[i2])*xa[x])*ya
			dstRow[x] = saturate(roundf(res))
		}
	}
	return out
}

// padReflect101 adds right and bottom borders like cv::BORDER_REFLECT_101.
// Like OpenCV, a side that already divides gets a whole extra tile's worth.
func padReflect101(g *Gray, padX, padY, tilesX, tilesY int) *Gray {
	out := NewGray(g.W+padX, g.H+padY)
	reflect := func(i, n int) int {
		if n == 1 {
			return 0
		}
		for i < 0 || i >= n {
			if i < 0 {
				i = -i
			}
			if i >= n {
				i = 2*n - 2 - i
			}
		}
		return i
	}
	for y := 0; y < out.H; y++ {
		sy := reflect(y, g.H)
		for x := 0; x < out.W; x++ {
			out.Pix[y*out.W+x] = g.Pix[sy*g.W+reflect(x, g.W)]
		}
	}
	return out
}
