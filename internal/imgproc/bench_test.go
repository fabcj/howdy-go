package imgproc

import (
	"fmt"
	"testing"
)

// Synthetic frames the size of an IR camera's (640x360) and a webcam's
func benchFrame(w, h int) *Gray {
	g := NewGray(w, h)
	for i := range g.Pix {
		g.Pix[i] = byte(40 + (i*7+i/w*3)%180)
	}
	return g
}

var sizes = [][2]int{{640, 360}, {1280, 720}}

func BenchmarkCLAHE(b *testing.B) {
	for _, s := range sizes {
		g := benchFrame(s[0], s[1])
		b.Run(fmt.Sprintf("%dx%d", s[0], s[1]), func(b *testing.B) {
			for b.Loop() {
				CLAHE(g, 2.0, 8, 8)
			}
		})
	}
}

func BenchmarkDarkness(b *testing.B) {
	g := benchFrame(640, 360)
	for b.Loop() {
		Darkness(g)
	}
}

func BenchmarkShrinkArea(b *testing.B) {
	g := benchFrame(1280, 720)
	for b.Loop() {
		ShrinkArea(g, 360.0/720)
	}
}
