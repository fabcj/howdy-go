package face

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand/v2"
	"os/exec"
	"testing"
)

func TestDistanceAgainstNumpy(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	if exec.Command(py, "-I", "-c", "import numpy").Run() != nil {
		t.Skip("no numpy")
	}
	r := rand.New(rand.NewPCG(5, 6))
	known := make([][]float64, 50)
	for i := range known {
		known[i] = make([]float64, 128)
		for j := range known[i] {
			known[i][j] = r.NormFloat64() * 0.1
		}
	}
	var probe [128]float64
	for j := range probe {
		probe[j] = float64(float32(r.NormFloat64() * 0.1))
	}
	in, _ := json.Marshal(map[string]any{"known": known, "probe": probe})
	cmd := exec.Command(py, "-I", "-c", `
import json, sys, numpy as np
d = json.load(sys.stdin)
m = np.linalg.norm(np.array(d["known"]) - np.array(d["probe"]), axis=1)
json.dump({"d": m.tolist(), "i": int(np.argmin(m))}, sys.stdout)`)
	cmd.Stdin = bytes.NewReader(in)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		D []float64
		I int
	}
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	for i, k := range known {
		if got := Distance(k, &probe); math.Float64bits(got) != math.Float64bits(want.D[i]) {
			t.Errorf("distance %d: %v, numpy %v", i, got, want.D[i])
		}
	}
	if _, i := Best(known, &probe); i != want.I {
		t.Errorf("best %d, numpy %d", i, want.I)
	}
}

func TestPairwiseSum(t *testing.T) {
	v := make([]float64, 300)
	for i := range v {
		v[i] = 1
	}
	for _, n := range []int{0, 1, 7, 8, 9, 128, 129, 300} {
		if got := PairwiseSum(v[:n]); got != float64(n) {
			t.Errorf("sum of %d ones = %v", n, got)
		}
	}
}
