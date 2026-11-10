package snapshot

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fabcj/howdy-go/internal/imgproc"
)

func frames(n int) []*imgproc.Gray {
	var out []*imgproc.Gray
	for i := 0; i < n; i++ {
		g := imgproc.NewGray(320, 180)
		for j := range g.Pix {
			g.Pix[j] = byte(j * (i + 1))
		}
		out = append(out, g)
	}
	return out
}

func TestBuild(t *testing.T) {
	logo := image.NewNRGBA(image.Rect(0, 0, 180, 57))
	for i := range logo.Pix {
		logo.Pix[i] = 200
	}
	lines := []string{"FAILED LOGIN", "Date: 2026/01/02 03:04:05 UTC"}
	img, err := Build(frames(3), lines, logo)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 960 || b.Dy() != 180+2*20+40 {
		t.Fatalf("size %v", b)
	}
	if c := img.RGBAAt(5, 230); c != (color.RGBA{44, 44, 44, 255}) {
		t.Errorf("padding is %v", c)
	}
	if c := img.RGBAAt(960-210+5, 180+20+5); c.R != 200 {
		t.Errorf("logo missing, got %v", c)
	}

	// No logo with a single frame
	img, err = Build(frames(1), lines, logo)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 320 {
		t.Errorf("width %d", img.Bounds().Dx())
	}

	if _, err := Build(nil, lines, nil); err == nil {
		t.Error("built from no frames")
	}
}

func TestSave(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "snapshots")
	img, err := Build(frames(2), []string{"x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	path, err := Save(dir, when, img)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "20260102T030405.jpg" {
		t.Errorf("name %s", path)
	}
	st, _ := os.Stat(dir)
	if st.Mode().Perm() != 0o750 {
		t.Errorf("dir mode %v", st.Mode())
	}
	f, _ := os.Open(path)
	if _, err := jpeg.Decode(f); err != nil {
		t.Errorf("not a JPEG: %v", err)
	}
	f.Close()

	// Same second: refused rather than overwritten
	if _, err := Save(dir, when, img); err == nil {
		t.Error("overwrote a snapshot")
	}

	// A planted symlink isn't followed
	target := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(target, []byte("keep"), 0o600)
	later := when.Add(time.Second)
	os.Symlink(target, filepath.Join(dir, Name(later)))
	if _, err := Save(dir, later, img); err == nil {
		t.Error("wrote through a symlink")
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Error("symlink target changed")
	}

	// Nor is a symlinked snapshots dir
	link := filepath.Join(t.TempDir(), "linked")
	os.Symlink(dir, link)
	if _, err := Save(link, later.Add(time.Second), img); err == nil {
		t.Error("followed a symlinked dir")
	}
}
