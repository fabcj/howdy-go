package model

import (
	"bytes"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Synthetic encodings only, never real face data
func fakeEncoding(r *rand.Rand) []float64 {
	enc := make([]float64, EncodingSize)
	for i := range enc {
		// dlib gives float32 widened to double, so mix both kinds
		if i%2 == 0 {
			enc[i] = float64(float32(r.NormFloat64() * 0.1))
		} else {
			enc[i] = r.NormFloat64() * 0.1
		}
	}
	enc[0] = 1e-05
	enc[1] = -0.0
	enc[2] = 0.25
	return enc
}

func sampleFile(t *testing.T) []byte {
	r := rand.New(rand.NewPCG(1, 2))
	m := &Models{}
	m.Entries = append(m.Entries,
		NewEntry(1730000000, "Model #0", 0, fakeEncoding(r)),
		NewEntry(1730000100, "Caf\u00e9 \"quoted\" \\ tab\t \U0001F600 \x7f", 3, fakeEncoding(r)))
	data, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRoundTrip(t *testing.T) {
	data := sampleFile(t)
	m, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, again) {
		t.Errorf("round trip changed the file")
	}
	encs, owner := m.Flat()
	if len(encs) != 2 || owner[1] != 1 {
		t.Errorf("Flat = %d encodings, owners %v", len(encs), owner)
	}
	if id, _ := m.NextID(); id != 4 {
		t.Errorf("NextID = %d", id)
	}
}

// What Python's json.dump(json.load(f)) writes, compared byte for byte.
// Skipped without python3.
func TestAgainstPython(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	inputs := [][]byte{
		sampleFile(t),
		// Reordered keys, an unknown key, an int in data, odd whitespace
		[]byte(`[ {"label":"x","data":[[` + strings.Repeat("0, ", 127) + `-0.5e-7]],"id":1,"extra":[true,false,null,{}],"time":1.5} ]`),
		[]byte(`[]`),
	}
	for i, in := range inputs {
		cmd := exec.Command(py, "-I", "-c", "import json,sys; sys.stdout.write(json.dumps(json.loads(sys.stdin.read())))")
		cmd.Stdin = bytes.NewReader(in)
		want, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		m, err := Parse(in)
		if err != nil {
			t.Errorf("input %d: %v", i, err)
			continue
		}
		got, err := m.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("input %d:\n go %s\npy  %s", i, got, want)
		}
	}
}

func TestRefused(t *testing.T) {
	enc := "[" + strings.Repeat("0.1, ", 127) + "0.1]"
	for name, in := range map[string]string{
		"not a list":     `{}`,
		"no data":        `[{"id": 0}]`,
		"short":          `[{"data": [[0.1, 0.2]]}]`,
		"nan":            `[{"data": [` + strings.Replace(enc, "0.1]", "NaN]", 1) + `]}]`,
		"infinity":       `[{"data": [` + strings.Replace(enc, "0.1]", "1e999]", 1) + `]}]`,
		"duplicate key":  `[{"data": [], "data": []}]`,
		"lone surrogate": `[{"label": "\ud800", "data": []}]`,
		"big int":        `[{"id": 99999999999999999999, "data": []}]`,
		"trailing":       `[] x`,
		"leading zero":   `[{"id": 01, "data": []}]`,
		"control":        "[{\"label\": \"a\x01\", \"data\": []}]",
		"deep":           strings.Repeat("[", 100) + strings.Repeat("]", 100),
	} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidUser(t *testing.T) {
	for _, ok := range []string{"alice", "_x", "a-b_c1", "machine$", strings.Repeat("a", 32)} {
		if !ValidUser(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "../x", "a/b", "Root", "1a", "a b", "x.dat", strings.Repeat("a", 33), "a$b"} {
		if ValidUser(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestStore(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	me := os.Getuid()
	if _, err := Load(dir, "alice", me); err != ErrNoModel {
		t.Fatalf("missing model: %v", err)
	}

	m, err := Parse(sampleFile(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, "alice", os.Getgid(), m); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "alice.dat"))
	if err != nil || st.Mode().Perm() != 0o640 {
		t.Fatalf("saved with %v, %v", st.Mode(), err)
	}
	if _, err := Load(dir, "alice", me); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "alice", me+1); err == nil {
		t.Error("loaded a model owned by someone else")
	}

	// A symlink in place of the model is refused
	if err := os.Symlink(filepath.Join(dir, "alice.dat"), filepath.Join(dir, "bob.dat")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "bob", me); err == nil {
		t.Error("followed a symlink")
	}

	// So is one writable by others
	if err := os.Chmod(filepath.Join(dir, "alice.dat"), 0o662); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "alice", me); err == nil {
		t.Error("loaded a world writable model")
	}

	// A planted tmp symlink doesn't redirect the write
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "carol.dat.tmp")); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, "carol", os.Getgid(), m); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Error("write went through the symlink")
	}
	var lst unix.Stat_t
	if err := unix.Lstat(filepath.Join(dir, "carol.dat"), &lst); err != nil || lst.Mode&unix.S_IFMT != unix.S_IFREG {
		t.Error("carol.dat isn't a regular file")
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte(`[{"time": 1, "label": "a", "id": 0, "data": [[0.5]]}]`))
	f.Add([]byte(`{"a": [1, 2.5, -0.0, "\u00e9\ud83d\ude00", true, null]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := Decode(data)
		if err != nil {
			return
		}
		out, err := Encode(v)
		if err != nil {
			t.Fatalf("decoded but can't encode: %v", err)
		}
		v2, err := Decode(out)
		if err != nil {
			t.Fatalf("own output doesn't decode: %v\n%s", err, out)
		}
		out2, err := Encode(v2)
		if err != nil || !bytes.Equal(out, out2) {
			t.Fatalf("encoding isn't stable:\n%s\n%s", out, out2)
		}
	})
}
