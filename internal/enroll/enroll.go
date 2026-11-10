// Package enroll turns the encodings howdy add collects into one model, the
// way the averaging enrollment in Howdy's cli/add.py does
package enroll

import (
	"errors"
	"math"
	"sort"

	"github.com/fabcj/howdy-go/internal/face"
)

// Result is the averaged encoding and how well the frames agreed
type Result struct {
	Encoding []float64
	Used     int
	Total    int
	Median   float64 // spread of the kept frames, in certainty units
	Worst    float64
}

// Average drops frames far from the rest (blinks, turning away) and
// averages the others. Distances are scaled by 10 to match "certainty".
func Average(encs [][]float64) (*Result, error) {
	n := len(encs)
	if n == 0 {
		return nil, errors.New("no encodings")
	}
	for _, e := range encs {
		if len(e) == 0 || len(e) != len(encs[0]) {
			return nil, errors.New("encodings of different lengths")
		}
	}
	mean := meanOf(encs, nil)

	dist := make([]float64, n)
	for i := range encs {
		dist[i] = face.Distance(mean, encs[i]) * 10
	}
	sorted := append([]float64(nil), dist...)
	sort.Float64s(sorted)
	limit := math.Max(median(sorted)*1.5, sorted[(n-1)/2])

	keep := make([]bool, n)
	used := 0
	for i, d := range dist {
		if d <= limit {
			keep[i] = true
			used++
		}
	}
	avg := meanOf(encs, keep)

	spread := []float64{}
	for i := range encs {
		if keep[i] {
			spread = append(spread, face.Distance(avg, encs[i])*10)
		}
	}
	sort.Float64s(spread)
	return &Result{
		Encoding: avg,
		Used:     used,
		Total:    n,
		Median:   median(spread),
		Worst:    spread[len(spread)-1],
	}, nil
}

// numpy's mean over axis 0: rows added in order, then divided by the count
func meanOf(encs [][]float64, keep []bool) []float64 {
	sum := make([]float64, len(encs[0]))
	count := 0
	for i := range encs {
		if keep != nil && !keep[i] {
			continue
		}
		for j, v := range encs[i] {
			sum[j] += v
		}
		count++
	}
	for j := range sum {
		sum[j] /= float64(count)
	}
	return sum
}

// numpy's median of sorted values
func median(sorted []float64) float64 {
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
