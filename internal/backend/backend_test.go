package backend

import (
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/fabcj/howdy-go/internal/config"
	"github.com/fabcj/howdy-go/internal/model"
	"github.com/fabcj/howdy-go/internal/paths"
)

// Synthetic descriptors only, never real face data
func vec(size int, first float64) []float64 {
	v := make([]float64, size)
	v[0] = first
	return v
}

// sandbox points the sface models dir at a temp dir the test user owns
func sandbox(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldDir, oldOwner := paths.SFaceModelsDir, paths.ModelOwner
	paths.SFaceModelsDir = dir
	paths.ModelOwner = strconv.Itoa(os.Geteuid())
	t.Cleanup(func() { paths.SFaceModelsDir, paths.ModelOwner = oldDir, oldOwner })
	return dir
}

func dat(entries ...*model.Object) *model.Models {
	m := model.NewModels(model.EncodingSize)
	m.Entries = append(m.Entries, entries...)
	return m
}

func saveSFace(t *testing.T, dir string, entries ...*model.Object) {
	t.Helper()
	m := model.NewModels(SFaceSize)
	m.Entries = append(m.Entries, entries...)
	if err := model.Save(dir, "alice", os.Getegid(), m); err != nil {
		t.Fatal(err)
	}
}

func TestKnownDlibUsesTheDatFile(t *testing.T) {
	d := dat(model.NewEntry(100, "a", 0, vec(model.EncodingSize, 1)),
		model.NewEntry(200, "b", 1, vec(model.EncodingSize, 2)))
	known, owner, err := Known(&config.Settings{Recognizer: "dlib"}, "alice", d)
	if err != nil || len(known) != 2 || owner[1] != 1 {
		t.Fatalf("known %d, owner %v, err %v", len(known), owner, err)
	}
}

func TestKnownSFaceWithoutAFileIsNotEnrolled(t *testing.T) {
	sandbox(t)
	d := dat(model.NewEntry(100, "a", 0, vec(model.EncodingSize, 1)))
	_, _, err := Known(&config.Settings{Recognizer: "sface"}, "alice", d)
	if !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("err = %v, want ErrNotEnrolled: sface must never fall back to dlib", err)
	}
}

func TestKnownSFaceFollowsTheDatFile(t *testing.T) {
	dir := sandbox(t)
	saveSFace(t, dir,
		model.NewTaggedEntry(100, "a", 0, vec(SFaceSize, 1), SFaceEncoder),
		model.NewTaggedEntry(200, "b", 1, vec(SFaceSize, 2), SFaceEncoder),
		// Same id as a live model but another time: a leftover of a removed one
		model.NewTaggedEntry(999, "stale", 1, vec(SFaceSize, 3), SFaceEncoder),
		// Another encoder's descriptor is never compared
		model.NewTaggedEntry(300, "c", 2, vec(SFaceSize, 4), "other-encoder"),
	)
	d := dat(model.NewEntry(100, "a", 0, vec(model.EncodingSize, 0)),
		model.NewEntry(200, "b", 1, vec(model.EncodingSize, 0)),
		model.NewEntry(300, "c", 2, vec(model.EncodingSize, 0)))

	known, owner, err := Known(&config.Settings{Recognizer: "sface"}, "alice", d)
	if err != nil {
		t.Fatal(err)
	}
	if len(known) != 2 || known[0][0] != 1 || known[1][0] != 2 || owner[0] != 0 || owner[1] != 1 {
		t.Errorf("known %v, owner %v", known, owner)
	}
}

func TestKnownSFaceWithEverythingRemovedFromTheDat(t *testing.T) {
	dir := sandbox(t)
	saveSFace(t, dir, model.NewTaggedEntry(100, "a", 0, vec(SFaceSize, 1), SFaceEncoder))
	d := dat(model.NewEntry(555, "other", 0, vec(model.EncodingSize, 0)))
	_, _, err := Known(&config.Settings{Recognizer: "sface"}, "alice", d)
	if !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("err = %v, want ErrNotEnrolled", err)
	}
}

func TestKnownSFaceRefusesAFileOthersCanWrite(t *testing.T) {
	dir := sandbox(t)
	saveSFace(t, dir, model.NewTaggedEntry(100, "a", 0, vec(SFaceSize, 1), SFaceEncoder))
	if err := os.Chmod(dir+"/alice.dat", 0o666); err != nil {
		t.Fatal(err)
	}
	d := dat(model.NewEntry(100, "a", 0, vec(model.EncodingSize, 0)))
	_, _, err := Known(&config.Settings{Recognizer: "sface"}, "alice", d)
	if err == nil || errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("err = %v, want a refusal that isn't ErrNotEnrolled", err)
	}
}

func TestMissingData(t *testing.T) {
	oldONNX, oldDlib := paths.ONNXDir, paths.DlibDataDir
	t.Cleanup(func() { paths.ONNXDir, paths.DlibDataDir = oldONNX, oldDlib })
	paths.ONNXDir, paths.DlibDataDir = t.TempDir(), t.TempDir()

	if MissingData("sface") == "" || MissingData("dlib") == "" {
		t.Error("empty data dirs reported as complete")
	}
	for _, f := range []string{YuNetFile, SFaceFile} {
		if err := os.WriteFile(paths.ONNXDir+"/"+f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if msg := MissingData("sface"); msg != "" {
		t.Errorf("sface: %s", msg)
	}
	if err := os.WriteFile(paths.ShapePredictor(), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg := MissingData("dlib"); msg != "" {
		t.Errorf("dlib: %s", msg)
	}
}
