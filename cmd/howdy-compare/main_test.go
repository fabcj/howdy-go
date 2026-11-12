package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fabcj/howdy-go/internal/compare"
	"github.com/fabcj/howdy-go/internal/model"
	"github.com/fabcj/howdy-go/internal/paths"
)

// The test binary runs howdy-compare itself when asked to, so the exit
// codes PAM reads are tested through a real process, with the same build
// tags and no camera, and without limiting the test process
const childEnv = "HOWDY_COMPARE_TEST_CHILD"

func TestMain(m *testing.M) {
	if dir := os.Getenv(childEnv); dir != "" {
		l := paths.Layout{
			ConfigDir:      filepath.Join(dir, "etc"),
			UserModelsDir:  filepath.Join(dir, "models"),
			SFaceModelsDir: filepath.Join(dir, "models-sface"),
			DlibDataDir:    filepath.Join(dir, "no-dlib-data"),
			ONNXDir:        filepath.Join(dir, "no-onnx"),
			ModelOwner:     os.Geteuid(),
		}
		os.Args = append([]string{"howdy-compare"}, os.Args[len(os.Args)-argCount():]...)
		os.Exit(run(l))
	}
	os.Exit(m.Run())
}

// The child's arguments come after "--" in its command line
func argCount() int {
	for i, a := range os.Args {
		if a == "--" {
			return len(os.Args) - i - 1
		}
	}
	return 0
}

type env struct {
	dir string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"etc", "models", "models-sface"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &env{dir}
}

func (e *env) config(t *testing.T, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.dir, "etc", "config.ini"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// model enrols a synthetic dlib model for alice
func (e *env) model(t *testing.T) {
	t.Helper()
	enc := make([]float64, model.EncodingSize)
	m := model.NewModels(model.EncodingSize)
	m.Entries = append(m.Entries, model.NewEntry(100, "test", 0, enc))
	if err := model.Save(filepath.Join(e.dir, "models"), "alice", os.Geteuid(), m); err != nil {
		t.Fatal(err)
	}
}

func (e *env) run(t *testing.T, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"--"}, args...)...)
	cmd.Env = append(os.Environ(), childEnv+"="+e.dir)
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

const baseConfig = "[core]\nrecognizer = dlib\n[video]\ndevice_path = /nonexistent/video\n"

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, e *env)
		args   []string
		want   int
		output string
	}{
		{"no arguments", func(t *testing.T, e *env) {}, nil, compare.Abort, ""},
		{"invalid username", func(t *testing.T, e *env) {}, []string{"../alice"}, compare.Abort, "invalid username"},
		{"no model file", func(t *testing.T, e *env) {}, []string{"alice"}, compare.NoFaceModel, ""},
		{"model without encodings", func(t *testing.T, e *env) {
			m := model.NewModels(model.EncodingSize)
			if err := model.Save(filepath.Join(e.dir, "models"), "alice", os.Geteuid(), m); err != nil {
				t.Fatal(err)
			}
		}, []string{"alice"}, compare.NoFaceModel, ""},
		{"model writable by others", func(t *testing.T, e *env) {
			e.model(t)
			if err := os.Chmod(filepath.Join(e.dir, "models", "alice.dat"), 0o666); err != nil {
				t.Fatal(err)
			}
		}, []string{"alice"}, compare.Abort, "writable by others"},
		{"no config", func(t *testing.T, e *env) { e.model(t) }, []string{"alice"}, compare.Abort, ""},
		{"duplicate config key", func(t *testing.T, e *env) {
			e.model(t)
			e.config(t, baseConfig+"timeout = 4\ntimeout = 5\n")
		}, []string{"alice"}, compare.Abort, ""},
		{"unknown recogniser", func(t *testing.T, e *env) {
			e.model(t)
			e.config(t, strings.Replace(baseConfig, "dlib", "arcface", 1))
		}, []string{"alice"}, compare.Abort, "recognizer"},
		{"rubberstamps on", func(t *testing.T, e *env) {
			e.model(t)
			e.config(t, baseConfig+"[rubberstamps]\nenabled = true\n")
		}, []string{"alice"}, compare.Abort, "rubberstamps"},
		{"sface without descriptors never falls back to dlib", func(t *testing.T, e *env) {
			e.model(t)
			e.config(t, strings.Replace(baseConfig, "dlib", "sface", 1))
		}, []string{"alice"}, compare.NoFaceModel, "no sface model"},
		{"camera path missing", func(t *testing.T, e *env) {
			e.model(t)
			e.config(t, baseConfig+"warn_no_device = false\n")
		}, []string{"alice"}, compare.BadDevice, ""},
		{"model data missing", func(t *testing.T, e *env) {
			e.model(t)
			e.config(t, "[core]\nrecognizer = dlib\n[video]\ndevice_path = "+os.Args[0]+"\n")
		}, []string{"alice"}, compare.Abort, "Data files have not been downloaded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			tt.setup(t, e)
			code, out := e.run(t, tt.args...)
			if code != tt.want {
				t.Errorf("exit %d, want %d\n%s", code, tt.want, out)
			}
			if !strings.Contains(out, tt.output) {
				t.Errorf("output lacks %q:\n%s", tt.output, out)
			}
		})
	}
}
