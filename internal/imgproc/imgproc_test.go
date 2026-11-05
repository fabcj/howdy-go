package imgproc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Synthetic test images: noise, gradients and a dark IR-like frame
func testImages() map[string]*Gray {
	r := rand.New(rand.NewPCG(7, 9))
	imgs := map[string]*Gray{}
	add := func(name string, w, h int, f func(x, y int) int) {
		g := NewGray(w, h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				g.Pix[y*w+x] = saturate(f(x, y))
			}
		}
		imgs[name] = g
	}
	add("noise640x360", 640, 360, func(x, y int) int { return r.IntN(256) })
	add("gradient640x360", 640, 360, func(x, y int) int { return (x + 2*y) / 4 })
	add("irlike640x360", 640, 360, func(x, y int) int {
		dx, dy := x-320, y-170
		v := 200 - (dx*dx+dy*dy)/300
		return v + r.IntN(9) - 4
	})
	add("dark640x480", 640, 480, func(x, y int) int { return r.IntN(30) })
	add("odd333x211", 333, 211, func(x, y int) int { return (x*y)%256 ^ r.IntN(16) })
	add("tall360x640", 360, 640, func(x, y int) int { return (x ^ y) & 0xff })
	add("flat100x50", 100, 50, func(x, y int) int { return 77 })
	add("odd335x213", 335, 213, func(x, y int) int { return r.IntN(256) })
	return imgs
}

const cvScript = `
import sys, numpy as np, cv2
op, w, h, src, dst = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), sys.argv[4], sys.argv[5]
img = np.fromfile(src, np.uint8).reshape(h, w)
if op == "clahe":
    out = cv2.createCLAHE(clipLimit=2.0, tileGridSize=(8, 8)).apply(img)
elif op.startswith("area"):
    f = float(op[4:])
    out = cv2.resize(img, None, fx=f, fy=f, interpolation=cv2.INTER_AREA)
elif op == "hist":
    hist = cv2.calcHist([img], [0], None, [8], [0, 256])
    out = np.array([hist[0] / np.sum(hist) * 100], dtype=np.float32).reshape(1, 1)
elif op == "ccw":
    out = cv2.rotate(img, cv2.ROTATE_90_COUNTERCLOCKWISE)
elif op == "cw":
    out = cv2.rotate(img, cv2.ROTATE_90_CLOCKWISE)
np.ascontiguousarray(out).tofile(dst)
sys.stdout.write("%d %d" % (out.shape[1] if out.ndim > 1 else 0, out.shape[0]))
`

// opencv runs cv2 on an image, skipping the test without python3 and cv2
func opencv(t *testing.T, op string, g *Gray) ([]byte, int, int) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	if exec.Command(py, "-I", "-c", "import cv2").Run() != nil {
		t.Skip("no cv2")
	}
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "in"), filepath.Join(dir, "out")
	if err := os.WriteFile(src, g.Pix, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(py, "-I", "-c", cvScript, op, fmt.Sprint(g.W), fmt.Sprint(g.H), src, dst).Output()
	if err != nil {
		t.Fatal(err)
	}
	var w, h int
	fmt.Sscan(string(out), &w, &h)
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	return data, w, h
}

func compare(t *testing.T, name string, got, want []byte, tolerance int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d pixels, OpenCV has %d", name, len(got), len(want))
	}
	worst, off := 0, 0
	for i := range got {
		d := int(got[i]) - int(want[i])
		if d < 0 {
			d = -d
		}
		if d > 0 {
			off++
		}
		worst = max(worst, d)
	}
	t.Logf("%s: %d of %d pixels differ, worst by %d", name, off, len(got), worst)
	if worst > tolerance {
		t.Errorf("%s: off by up to %d, allowed %d", name, worst, tolerance)
	}
}

func TestCLAHEMatchesOpenCV(t *testing.T) {
	for name, g := range testImages() {
		want, _, _ := opencv(t, "clahe", g)
		compare(t, name, CLAHE(g, 2.0, 8, 8).Pix, want, 1)
	}
}

func TestShrinkMatchesOpenCV(t *testing.T) {
	for name, g := range testImages() {
		for _, f := range []float64{0.5, 0.25, 320.0 / 480, 320.0 / 360, 0.6, 0.37, 0.75} {
			want, w, h := opencv(t, fmt.Sprintf("area%v", f), g)
			got := ShrinkArea(g, f)
			if got.W != w || got.H != h {
				t.Errorf("%s x%v: size %dx%d, OpenCV %dx%d", name, f, got.W, got.H, w, h)
				continue
			}
			compare(t, fmt.Sprintf("%s x%v", name, f), got.Pix, want, 1)
		}
	}
}

func TestDarknessMatchesOpenCV(t *testing.T) {
	for name, g := range testImages() {
		want, _, _ := opencv(t, "hist", g)
		var w float32
		binary.Read(bytes.NewReader(want), binary.LittleEndian, &w)
		if got, _ := Darkness(g); got != w {
			t.Errorf("%s: darkness %v, numpy %v", name, got, w)
		}
	}
}

func TestRotateMatchesOpenCV(t *testing.T) {
	g := testImages()["odd333x211"]
	want, _, _ := opencv(t, "ccw", g)
	if !bytes.Equal(RotateCCW(g).Pix, want) {
		t.Error("counter-clockwise differs")
	}
	want, _, _ = opencv(t, "cw", g)
	if !bytes.Equal(RotateCW(g).Pix, want) {
		t.Error("clockwise differs")
	}
}
