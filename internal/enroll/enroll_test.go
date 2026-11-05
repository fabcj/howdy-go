package enroll

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand/v2"
	"os/exec"
	"testing"
)

// Synthetic encodings only, never real face data
func fake(r *rand.Rand, n int) [][128]float64 {
	base := [128]float64{}
	for j := range base {
		base[j] = r.NormFloat64() * 0.1
	}
	out := make([][128]float64, n)
	for i := range out {
		for j := range base {
			// float32 values, like dlib's
			out[i][j] = float64(float32(base[j] + r.NormFloat64()*0.01))
		}
	}
	// One outlier, as from a blink
	for j := range out[0] {
		out[0][j] += 0.05
	}
	return out
}

const pyScript = `
import json, sys, numpy as np
enc = np.array(json.load(sys.stdin))
distances = np.linalg.norm(enc - enc.mean(axis=0), axis=1) * 10
keep = distances <= max(np.median(distances) * 1.5, np.sort(distances)[(len(enc) - 1) // 2])
face_encoding = enc[keep].mean(axis=0)
spread = np.linalg.norm(enc[keep] - face_encoding, axis=1) * 10
json.dump({"enc": face_encoding.tolist(), "used": int(keep.sum()),
           "median": float(np.median(spread)), "worst": float(spread.max())}, sys.stdout)
`

func TestAgainstNumpy(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	if exec.Command(py, "-I", "-c", "import numpy").Run() != nil {
		t.Skip("no numpy")
	}
	r := rand.New(rand.NewPCG(3, 4))
	for _, n := range []int{1, 2, 3, 4, 15, 16} {
		encs := fake(r, n)
		in, _ := json.Marshal(encs)
		cmd := exec.Command(py, "-I", "-c", pyScript)
		cmd.Stdin = bytes.NewReader(in)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		var want struct {
			Enc    []float64
			Used   int
			Median float64
			Worst  float64
		}
		if err := json.Unmarshal(out, &want); err != nil {
			t.Fatal(err)
		}
		got, err := Average(encs)
		if err != nil {
			t.Fatal(err)
		}
		if got.Used != want.Used || got.Median != want.Median || got.Worst != want.Worst {
			t.Errorf("n=%d: used %d median %v worst %v, numpy %d %v %v", n, got.Used, got.Median, got.Worst, want.Used, want.Median, want.Worst)
		}
		for j := range want.Enc {
			if math.Float64bits(got.Encoding[j]) != math.Float64bits(want.Enc[j]) {
				t.Errorf("n=%d: encoding[%d] %v, numpy %v", n, j, got.Encoding[j], want.Enc[j])
				break
			}
		}
	}
}
