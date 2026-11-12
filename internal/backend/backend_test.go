package backend

import (
	"errors"
	"os"
	"testing"

	"github.com/fabcj/howdy-go/internal/config"
	"github.com/fabcj/howdy-go/internal/model"
	"github.com/fabcj/howdy-go/internal/paths"
	"github.com/fabcj/howdy-go/internal/sface/spec"
)

// Synthetic descriptors only, never real face data
func vec(size int, first float64) []float64 {
	v := make([]float64, size)
	v[0] = first
	return v
}

// sandbox is a layout whose sface models dir is a temp dir the test user owns
func sandbox(t *testing.T) paths.Layout {
	t.Helper()
	return paths.Layout{SFaceModelsDir: t.TempDir(), ModelOwner: os.Geteuid()}
}

func dat(entries ...*model.Entry) *model.Models {
	m := model.NewModels(model.EncodingSize)
	m.Entries = append(m.Entries, entries...)
	return m
}

func saveSFace(t *testing.T, dir string, entries ...*model.Entry) {
	t.Helper()
	m := model.NewModels(spec.Size)
	m.Entries = append(m.Entries, entries...)
	if err := model.Save(dir, "alice", os.Geteuid(), m); err != nil {
		t.Fatal(err)
	}
}

func TestKnownDlibUsesTheDatFile(t *testing.T) {
	d := dat(model.NewEntry(100, "a", 0, vec(model.EncodingSize, 1)),
		model.NewEntry(200, "b", 1, vec(model.EncodingSize, 2)))
	known, owner, err := Known(paths.Layout{}, &config.Settings{Recognizer: "dlib"}, "alice", d)
	if err != nil || len(known) != 2 || owner[1] != 1 {
		t.Fatalf("known %d, owner %v, err %v", len(known), owner, err)
	}
}

func TestKnownSFaceWithoutAFileIsNotEnrolled(t *testing.T) {
	l := sandbox(t)
	d := dat(model.NewEntry(100, "a", 0, vec(model.EncodingSize, 1)))
	_, _, err := Known(l, &config.Settings{Recognizer: "sface"}, "alice", d)
	if !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("err = %v, want ErrNotEnrolled: sface must never fall back to dlib", err)
	}
}

func TestKnownSFaceFollowsTheDatFile(t *testing.T) {
	l := sandbox(t)
	dir := l.SFaceModelsDir
	saveSFace(t, dir,
		model.NewTaggedEntry(100, "a", 0, vec(spec.Size, 1), spec.Encoder),
		model.NewTaggedEntry(200, "b", 1, vec(spec.Size, 2), spec.Encoder),
		// Same id as a live model but another time: a leftover of a removed one
		model.NewTaggedEntry(999, "stale", 1, vec(spec.Size, 3), spec.Encoder),
		// Another encoder's descriptor is never compared
		model.NewTaggedEntry(300, "c", 2, vec(spec.Size, 4), "other-encoder"),
	)
	d := dat(model.NewEntry(100, "a", 0, vec(model.EncodingSize, 0)),
		model.NewEntry(200, "b", 1, vec(model.EncodingSize, 0)),
		model.NewEntry(300, "c", 2, vec(model.EncodingSize, 0)))

	known, owner, err := Known(l, &config.Settings{Recognizer: "sface"}, "alice", d)
	if err != nil {
		t.Fatal(err)
	}
	if len(known) != 2 || known[0][0] != 1 || known[1][0] != 2 || owner[0] != 0 || owner[1] != 1 {
		t.Errorf("known %v, owner %v", known, owner)
	}
}

func TestKnownSFaceWithEverythingRemovedFromTheDat(t *testing.T) {
	l := sandbox(t)
	dir := l.SFaceModelsDir
	saveSFace(t, dir, model.NewTaggedEntry(100, "a", 0, vec(spec.Size, 1), spec.Encoder))
	d := dat(model.NewEntry(555, "other", 0, vec(model.EncodingSize, 0)))
	_, _, err := Known(l, &config.Settings{Recognizer: "sface"}, "alice", d)
	if !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("err = %v, want ErrNotEnrolled", err)
	}
}

func TestKnownSFaceRefusesAFileOthersCanWrite(t *testing.T) {
	l := sandbox(t)
	dir := l.SFaceModelsDir
	saveSFace(t, dir, model.NewTaggedEntry(100, "a", 0, vec(spec.Size, 1), spec.Encoder))
	if err := os.Chmod(dir+"/alice.dat", 0o666); err != nil {
		t.Fatal(err)
	}
	d := dat(model.NewEntry(100, "a", 0, vec(model.EncodingSize, 0)))
	_, _, err := Known(l, &config.Settings{Recognizer: "sface"}, "alice", d)
	if err == nil || errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("err = %v, want a refusal that isn't ErrNotEnrolled", err)
	}
}

func TestMissingData(t *testing.T) {
	l := paths.Layout{ONNXDir: t.TempDir(), DlibDataDir: t.TempDir()}

	if MissingData(l, "sface") == "" || MissingData(l, "dlib") == "" {
		t.Error("empty data dirs reported as complete")
	}
	for _, f := range []string{spec.YuNetFile, spec.SFaceFile} {
		if err := os.WriteFile(l.ONNXDir+"/"+f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if msg := MissingData(l, "sface"); msg != "" {
		t.Errorf("sface: %s", msg)
	}
	if err := os.WriteFile(l.ShapePredictor(), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg := MissingData(l, "dlib"); msg != "" {
		t.Errorf("dlib: %s", msg)
	}
}
