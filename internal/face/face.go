// Package face is what compare and the CLI need from a face recogniser,
// so the frame loop can be tested without dlib
package face

import (
	"math"

	"github.com/fabcj/howdy-go/internal/imgproc"
)

// Rect is a face box, edges included as in dlib. Detectors that find the
// eyes, nose and mouth corners too (YuNet) set Landmarks, in image order:
// the eye on the image's left first, then the other eye, the nose tip and
// the mouth corners in the same order.
type Rect struct {
	Left, Top, Right, Bottom int
	Landmarks                [5][2]float32
	HasLandmarks             bool
}

// Encoding is a face descriptor and the 5 landmarks it was aligned with.
// How long the descriptor is depends on the recogniser, 128 for dlib.
type Encoding struct {
	Descriptor []float64
	Parts      [5][2]int
}

// Recognizer finds faces and describes them. How it detects (dlib's
// upsample, HOG or CNN) is fixed when it's loaded.
type Recognizer interface {
	Detect(g *imgproc.Gray) ([]Rect, error)
	// Encode returns a new Descriptor every call: howdy add keeps them
	Encode(g *imgproc.Gray, r Rect) (Encoding, error)
	Close()
}

// Matcher is how a loop judges a distance and describes it: dlib users
// read a certainty, sface users a similarity. backend.MatcherFor builds one
// from the config.
type Matcher struct {
	Threshold float64                    // a face matches under this distance
	Unit      string                     // "Certainty" or "Similarity"
	Shown     func(dist float64) float64 // what to print for a distance
	NoFace    string                     // what to print when no face was seen
	Digits    int                        // decimals Shown is rounded to in snapshots
}

// Distance is numpy.linalg.norm(a - b). It's NaN when the lengths differ,
// which no certainty check passes.
//
// For 8 to 128 values, every descriptor here, numpy's pairwise summation
// is one 8 lane loop, applied straight to the squared differences. The
// float64 conversions keep the compiler from fusing d*d into the add,
// which would round differently from numpy.
func Distance(a, b []float64) float64 {
	if len(a) != len(b) {
		return math.NaN()
	}
	n := len(a)
	if n < 8 || n > 128 {
		sq := make([]float64, n)
		for i, v := range a {
			d := v - b[i]
			sq[i] = float64(d * d)
		}
		return math.Sqrt(PairwiseSum(sq))
	}
	var r [8]float64
	for j := range r {
		d := a[j] - b[j]
		r[j] = float64(d * d)
	}
	i := 8
	for ; i < n-n%8; i += 8 {
		for j := range r {
			d := a[i+j] - b[i+j]
			r[j] += float64(d * d)
		}
	}
	res := ((r[0] + r[1]) + (r[2] + r[3])) + ((r[4] + r[5]) + (r[6] + r[7]))
	for ; i < n; i++ {
		d := a[i] - b[i]
		res += float64(d * d)
	}
	return math.Sqrt(res)
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

// CosineDistance is the Euclidean distance between two unit vectors whose
// cosine similarity is cos. Matching by one is matching by the other.
func CosineDistance(cos float64) float64 {
	return math.Sqrt(2 - 2*cos)
}

// Cosine undoes CosineDistance
func Cosine(dist float64) float64 {
	return 1 - dist*dist/2
}

// Unit scales v to length 1, as an averaged unit descriptor needs
func Unit(v []float64) []float64 {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	n := math.Sqrt(sum)
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = x / n
	}
	return out
}

// Best returns the smallest distance from enc to any of the known encodings
// and its index, like numpy's argmin (the first one on a tie). index is -1
// with no known encodings.
func Best(known [][]float64, enc []float64) (dist float64, index int) {
	index = -1
	for i, k := range known {
		d := Distance(k, enc)
		if index < 0 || d < dist {
			dist, index = d, i
		}
	}
	return dist, index
}
