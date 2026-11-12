package face

import (
	"math"
	"math/rand/v2"
	"testing"
)

// distanceRef is Distance the slow way: the squares written out, then
// numpy's pairwise sum over them
func distanceRef(a, b []float64) float64 {
	sq := make([]float64, len(a))
	for i, v := range a {
		d := v - b[i]
		sq[i] = float64(d * d)
	}
	return math.Sqrt(PairwiseSum(sq))
}

func randVec(rng *rand.Rand, n int) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = rng.NormFloat64() * 0.1
	}
	return v
}

// The 8 lane loop has to give the same bits as summing the written out
// squares, or norms stop matching numpy's
func TestDistanceBitExact(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, n := range []int{1, 7, 8, 9, 15, 16, 100, 127, 128, 129, 512} {
		for range 2000 {
			a, b := randVec(rng, n), randVec(rng, n)
			if x, y := Distance(a, b), distanceRef(a, b); math.Float64bits(x) != math.Float64bits(y) {
				t.Fatalf("n=%d: %v != %v", n, x, y)
			}
		}
	}
	if !math.IsNaN(Distance(make([]float64, 3), make([]float64, 4))) {
		t.Error("different lengths didn't give NaN")
	}
}

func BenchmarkDistance(b *testing.B) {
	rng := rand.New(rand.NewPCG(1, 2))
	x, y := randVec(rng, 128), randVec(rng, 128)
	for b.Loop() {
		Distance(x, y)
	}
}
