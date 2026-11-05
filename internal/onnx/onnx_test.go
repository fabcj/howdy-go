package onnx

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Models from tools/fetch-face-models, in HOWDY_GO_MODELS
func modelPath(t *testing.T, name string) string {
	dir := os.Getenv("HOWDY_GO_MODELS")
	if dir == "" {
		t.Skip("HOWDY_GO_MODELS not set")
	}
	return filepath.Join(dir, name)
}

const sfaceSHA = "0ba9fbfa01b5270c96627c4ef784da859931e02f04419c829e83484087c34e79"

func TestRunSFace(t *testing.T) {
	if !Available {
		t.Skip("no ONNX Runtime")
	}
	start := time.Now()
	s, err := Open(modelPath(t, "sface_2021dec.onnx"), sfaceSHA)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t.Logf("loaded in %v, output %q", time.Since(start), s.Output0())
	in := make([]float32, 3*112*112)
	for i := range in {
		in[i] = float32(i % 251)
	}
	start = time.Now()
	out, err := s.Run(in, []int64{1, 3, 112, 112}, []string{s.Output0()}, []int{128})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ran in %v", time.Since(start))
	if len(out[0]) != 128 {
		t.Fatalf("%d values, want 128", len(out[0]))
	}
	if _, err := s.Run(in, []int64{1, 3, 112, 112}, []string{s.Output0()}, []int{64}); err == nil {
		t.Error("an output bigger than its room didn't fail")
	}
}

func TestPinnedHashRefused(t *testing.T) {
	p := modelPath(t, "sface_2021dec.onnx")
	if _, err := Open(p, "00"+sfaceSHA[2:]); err == nil {
		t.Fatal("a model with the wrong hash loaded")
	}
	link := filepath.Join(t.TempDir(), "link.onnx")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link, sfaceSHA); err == nil {
		t.Fatal("a model through a symlink loaded")
	}
}
