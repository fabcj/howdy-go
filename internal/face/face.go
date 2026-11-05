// Package face is what compare and the CLI need from a face recogniser,
// so the frame loop can be tested without dlib
package face

import (
	"math"

	"howdy-go/internal/imgproc"
)

// Rect is a face box, edges included as in dlib
type Rect struct {
	Left, Top, Right, Bottom int
}

// Encoding is a face descriptor and the 5 landmarks it was aligned with
type Encoding struct {
	Descriptor [128]float64
	Parts      [5][2]int
}

// Recognizer finds faces and describes them
type Recognizer interface {
	Detect(g *imgproc.Gray, upsample int, useCNN bool) ([]Rect, error)
	Encode(g *imgproc.Gray, r Rect) (Encoding, error)
	Close()
}

// Distance is numpy.linalg.norm(a - b)
func Distance(a []float64, b *[128]float64) float64 {
	var sq [128]float64
	for i, v := range a {
		d := v - b[i]
		sq[i] = d * d
	}
	return math.Sqrt(PairwiseSum(sq[:len(a)]))
}

// PairwiseSum adds up v in the order numpy's pairwise summation does, so
// norms come out bit for bit the same
func PairwiseSum(v []float64) float64 {
	n := len(v)
	switch {
	case n < 8:
		res := 0.0
		for _, x := range v {
			res += x
		}
		return res
	case n <= 128:
		var r [8]float64
		copy(r[:], v[:8])
		i := 8
		for ; i < n-n%8; i += 8 {
			for j := range r {
				r[j] += v[i+j]
			}
		}
		res := ((r[0] + r[1]) + (r[2] + r[3])) + ((r[4] + r[5]) + (r[6] + r[7]))
		for ; i < n; i++ {
			res += v[i]
		}
		return res
	}
	m := n / 2
	m -= m % 8
	return PairwiseSum(v[:m]) + PairwiseSum(v[m:])
}

// Best returns the smallest distance from enc to any of the known encodings
// and its index, like numpy's argmin (the first one on a tie). index is -1
// with no known encodings.
func Best(known [][]float64, enc *[128]float64) (dist float64, index int) {
	index = -1
	for i, k := range known {
		d := Distance(k, enc)
		if index < 0 || d < dist {
			dist, index = d, i
		}
	}
	return dist, index
}
