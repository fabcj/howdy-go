// Package pyfmt prints numbers the way Howdy's Python code does, so messages,
// snapshots and model files come out the same.
package pyfmt

import (
	"math"
	"strconv"
	"strings"
)

// Float formats f like Python's repr() and str(): the shortest string that
// reads back as f, always with a ".0" or an exponent.
func Float(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}

	// d.dddde±x, from which Python picks fixed or exponent notation
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mant, exp, _ := strings.Cut(e, "e")
	x, _ := strconv.Atoi(exp)

	if x < -4 || x >= 16 {
		mant = strings.TrimSuffix(mant, ".0")
		sign := "+"
		if x < 0 {
			sign = "-"
			x = -x
		}
		xs := strconv.Itoa(x)
		if len(xs) < 2 {
			xs = "0" + xs
		}
		return mant + "e" + sign + xs
	}

	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsRune(s, '.') {
		s += ".0"
	}
	return s
}

// Round is Python's round(f, n) for a float, correctly rounded from the
// exact binary value.
func Round(f float64, n int) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return f
	}
	r, err := strconv.ParseFloat(strconv.FormatFloat(f, 'f', n, 64), 64)
	if err != nil {
		return f
	}
	return r
}

// Float32 formats a numpy float32 scalar the way str() does: the shortest
// digits that read back as the same float32, otherwise like Float
func Float32(f float32) string {
	v := float64(f)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return Float(v)
	}
	a := math.Abs(v)
	if a != 0 && (a < 1e-4 || a >= 1e8) {
		e := strconv.FormatFloat(v, 'e', -1, 32)
		mant, exp, _ := strings.Cut(e, "e")
		mant = strings.TrimSuffix(mant, ".0")
		return mant + "e" + exp
	}
	s := strconv.FormatFloat(v, 'f', -1, 32)
	if !strings.ContainsRune(s, '.') {
		s += ".0"
	}
	return s
}

// Float32Array formats a one element float32 numpy array the way str() does
// with numpy's default print options, e.g. "[55.113888]" or "[60.]".
func Float32Array(f float32) string {
	return "[" + float32Elem(f) + "]"
}

func float32Elem(f float32) string {
	v := float64(f)
	switch {
	case math.IsNaN(v):
		return "nan"
	case math.IsInf(v, 1):
		return "inf"
	case math.IsInf(v, -1):
		return "-inf"
	}

	// numpy switches the whole array to scientific for tiny or huge values
	a := math.Abs(v)
	if a != 0 && (a < 1e-4 || a >= 1e8) {
		s := strconv.FormatFloat(v, 'e', -1, 32)
		mant, exp, _ := strings.Cut(s, "e")
		if len(mant) > 10 {
			s = strconv.FormatFloat(v, 'e', 8, 32)
			mant, exp, _ = strings.Cut(s, "e")
			mant = strings.TrimRight(mant, "0")
		}
		if !strings.ContainsRune(mant, '.') {
			mant += "."
		}
		return mant + "e" + exp
	}

	s := strconv.FormatFloat(v, 'f', -1, 32)
	if i := strings.IndexByte(s, '.'); i >= 0 && len(s)-i-1 > 8 {
		s = strings.TrimRight(strconv.FormatFloat(v, 'f', 8, 32), "0")
	}
	if !strings.ContainsRune(s, '.') {
		s += "."
	}
	return s
}
