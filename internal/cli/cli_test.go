package cli

import (
	"bytes"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fabcj/howdy-go/internal/backend"
	"github.com/fabcj/howdy-go/internal/compare"
	"github.com/fabcj/howdy-go/internal/config"
	"github.com/fabcj/howdy-go/internal/face"
	"github.com/fabcj/howdy-go/internal/imgproc"
	"github.com/fabcj/howdy-go/internal/model"
	"github.com/fabcj/howdy-go/internal/paths"
	"github.com/fabcj/howdy-go/internal/pin"
	"github.com/fabcj/howdy-go/internal/sface/spec"
)

func TestParse(t *testing.T) {
	for _, c := range []struct {
		argv []string
		cmd  string
		args []string
		user string
		y    bool
		err  string
	}{
		{argv: []string{"add", "lbl", "-y", "-U", "x"}, cmd: "add", args: []string{"lbl"}, user: "x", y: true},
		{argv: []string{"add", "-y", "lbl"}, cmd: "add", args: []string{"lbl"}, user: "me", y: true},
		{argv: []string{"-y", "add", "lbl"}, cmd: "add", args: []string{"lbl"}, user: "me", y: true},
		{argv: []string{"list", "--plain", "-U", "x"}, cmd: "list", user: "x"},
		{argv: []string{"set", "certainty", "3"}, cmd: "set", args: []string{"certainty", "3"}, user: "me"},
		{argv: []string{"--user=bob", "list"}, cmd: "list", user: "bob"},
		{argv: []string{"add", "--", "-dash"}, cmd: "add", args: []string{"-dash"}, user: "me"},
		{argv: []string{"add", "a", "-y", "b"}, err: "unrecognized arguments: b"},
		{argv: []string{"bogus"}, err: "argument command: invalid choice: 'bogus' (choose from 'add', 'clear', 'config', 'disable', 'list', 'pin', 'remove', 'set', 'snapshot', 'test', 'version')"},
		{argv: []string{"-U"}, err: "argument -U/--user: expected one argument"},
		{argv: []string{"--nope", "list"}, err: "unrecognized arguments: --nope"},
	} {
		a, err := parse(c.argv, "me")
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("%v: error %v, want %q", c.argv, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v: %v", c.argv, err)
			continue
		}
		if a.command != c.cmd || strings.Join(a.arguments, " ") != strings.Join(c.args, " ") || a.user != c.user || a.yes != c.y {
			t.Errorf("%v: got %+v", c.argv, a)
		}
	}
}

// testEnv points paths at a scratch tree and fakes everything else
type testEnv struct {
	*Env
	out, errOut bytes.Buffer
	dir         string
	frames      []*imgproc.Gray
	faces       int
	edited      []string
	loaded      []string // recognisers Faces was asked for
	clock       time.Time
	// a fake ctrl+C once the camera has given this many frames
	stopAfter int
	stop      chan os.Signal
	colour    bool     // what the fake camera's Colour says
	secrets   []string // what the next ReadHidden calls return
}

func setup(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	l := paths.Layout{
		ConfigDir:      dir,
		UserModelsDir:  filepath.Join(dir, "models"),
		DlibDataDir:    filepath.Join(dir, "dlib-data"),
		LogPath:        filepath.Join(dir, "log"),
		DataDir:        dir,
		ModelOwner:     os.Getuid(),
		ONNXDir:        filepath.Join(dir, "onnx"),
		SFaceModelsDir: filepath.Join(dir, "models-sface"),
		PinDir:         filepath.Join(dir, "pin"),
		PinUserDir:     filepath.Join(dir, "pin-user"),
	}
	os.Chmod(dir, 0o755)
	os.Mkdir(l.DlibDataDir, 0o755)
	os.WriteFile(l.ShapePredictor(), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "video0"), nil, 0o644)
	os.WriteFile(l.ConfigFile(), []byte("[core]\ndisabled = false\n\n[video]\ncertainty = 3.5\ndevice_path = "+
		filepath.Join(dir, "video0")+"\nenroll_frames = 5\ndark_threshold = 60\n"), 0o644)

	te := &testEnv{dir: dir, clock: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), stop: make(chan os.Signal, 1)}
	te.Env = &Env{
		Paths:       l,
		Getenv:      func(k string) string { return map[string]string{"SUDO_USER": "alice"}[k] },
		Stdin:       strings.NewReader(""),
		Stdout:      &te.out,
		Stderr:      &te.errOut,
		Euid:        0,
		UserName:    func(string) string { return "" },
		CurrentUser: func() string { return "root" },
		UserGID:     func(string) (int, error) { return os.Getgid(), nil },
		UserUID:     func(string) (int, error) { return os.Getuid(), nil },
		ReadHidden: func(prompt string) ([]byte, error) {
			if len(te.secrets) == 0 {
				return nil, errors.New("no more secrets")
			}
			s := te.secrets[0]
			te.secrets = te.secrets[1:]
			return []byte(s), nil
		},
		Camera: func(s *config.Settings) (compare.Camera, func(), error) {
			return &fakeCam{te: te}, func() {}, nil
		},
		Faces: func(s *config.Settings, name config.Recognizer) (face.Recognizer, error) {
			te.loaded = append(te.loaded, string(name))
			return &fakeFaces{te: te}, nil
		},
		Edit: func(argv []string) error {
			te.edited = argv
			return nil
		},
		LookPath: func(name string) (string, error) {
			if name == "nano" {
				return "/usr/bin/nano", nil
			}
			return "", errors.New("not found")
		},
		Now:        func() time.Time { return te.clock },
		Sleep:      func(d time.Duration) { te.clock = te.clock.Add(d) },
		Interrupts: func() (<-chan os.Signal, func()) { return te.stop, func() {} },
	}
	return te
}

type fakeCam struct {
	te *testEnv
	i  int
}

func (c *fakeCam) Read(time.Duration) (*imgproc.Gray, error) {
	c.te.clock = c.te.clock.Add(70 * time.Millisecond)
	f := c.te.frames[c.i%len(c.te.frames)]
	c.i++
	if c.i == c.te.stopAfter {
		c.te.stop <- os.Interrupt
	}
	return f, nil
}

func (c *fakeCam) SetExposure(int) error { return nil }

func (c *fakeCam) Colour() bool { return c.te.colour }

type fakeFaces struct {
	te *testEnv
	n  int
}

func (f *fakeFaces) Detect(*imgproc.Gray) ([]face.Rect, error) {
	return make([]face.Rect, f.te.faces), nil
}

// Synthetic descriptors that differ a little per frame
func (f *fakeFaces) Encode(*imgproc.Gray, face.Rect) (face.Encoding, error) {
	e := face.Encoding{Descriptor: make([]float64, 128)}
	for i := range e.Descriptor {
		e.Descriptor[i] = float64(float32(0.01*float64(i%7) + 0.001*float64(f.n%3)))
	}
	f.n++
	return e, nil
}

func (f *fakeFaces) Close() {}

func litFrame() *imgproc.Gray {
	g := imgproc.NewGray(64, 36)
	for i := range g.Pix {
		g.Pix[i] = byte(100 + i%50)
	}
	return g
}

func (te *testEnv) run(args ...string) int {
	te.out.Reset()
	te.errOut.Reset()
	te.Args = args
	return Main(te.Env)
}

func TestNotRoot(t *testing.T) {
	te := setup(t)
	te.Euid = 1000
	if code := te.run("list"); code != 1 || te.out.String() != "Please run this command as root:\n\n\tsudo howdy list\n" {
		t.Errorf("exit %d: %q", code, te.out.String())
	}
	if code := te.run(); code != 0 || !strings.HasPrefix(te.out.String(), "current active user: alice\n\nusage: howdy") {
		t.Errorf("exit %d: %q", code, te.out.String())
	}
}

func TestBadUser(t *testing.T) {
	te := setup(t)
	if code := te.run("list", "-U", "../etc/x"); code != 1 {
		t.Errorf("path in username: exit %d", code)
	}
	if code := te.run("list", "-U", "root"); code != 1 || !strings.Contains(te.out.String(), "Can't run howdy commands as root") {
		t.Errorf("root: exit %d %q", code, te.out.String())
	}
}

func TestAddListRemove(t *testing.T) {
	te := setup(t)
	te.frames = []*imgproc.Gray{litFrame()}
	te.faces = 1

	if code := te.run("list"); code != 1 || !strings.Contains(te.out.String(), "Face models have not been initialized yet") {
		t.Errorf("list before add: exit %d %q", code, te.out.String())
	}

	if code := te.run("add", "Desk, left", "-y"); code != 0 {
		t.Fatalf("add: exit %d\n%s%s", code, te.out.String(), te.errOut.String())
	}
	for _, want := range []string{
		"No face model folder found, creating one\n",
		"Adding face model for the user alice\n",
		"Using default label \"Desk, left\" because of -y flag\n",
		"NOTICE: Removing illegal character \",\" from model name\n",
		"\nPlease look straight into the camera\n",
		"\nUsed 5 of 5 frames, spread ",
		"\nScan complete\nAdded a new model to alice\n",
	} {
		if !strings.Contains(te.out.String(), want) {
			t.Errorf("add output lacks %q:\n%s", want, te.out.String())
		}
	}
	st, err := os.Stat(filepath.Join(te.Paths.UserModelsDir, "alice.dat"))
	if err != nil || st.Mode().Perm() != 0o640 {
		t.Fatalf("model file %v %v", st, err)
	}

	// A second one, with a typed label cut to 24 characters
	te.Stdin = strings.NewReader("a label that is far too long for it\n")
	if code := te.run("add"); code != 0 {
		t.Fatalf("second add: exit %d", code)
	}

	te.run("list", "--plain")
	lines := strings.Split(te.out.String(), "\n")
	if len(lines) != 4 || lines[3] != "" || lines[2] != "" {
		t.Fatalf("plain list:\n%q", te.out.String())
	}
	day := time.Unix(te.clock.Unix(), 0).Local().Format("2006-01-02")
	if !strings.HasPrefix(lines[0], "0,"+day) || !strings.HasSuffix(lines[0], ",Desk left") ||
		!strings.HasSuffix(lines[1], ",a label that is far too ") {
		t.Errorf("plain list:\n%q", te.out.String())
	}

	te.run("list")
	if !strings.HasPrefix(te.out.String(), "Known face models for alice:\n\n\033[1;29mID  Date                 Label\033[0m\n0   ") {
		t.Errorf("list:\n%q", te.out.String())
	}

	if code := te.run("remove", "7", "-y"); code != 1 || te.out.String() != "No model with ID 7 exists for alice\n" {
		t.Errorf("remove missing: %d %q", code, te.out.String())
	}
	te.Stdin = strings.NewReader("n\n")
	if code := te.run("remove", "0"); code != 1 || !strings.Contains(te.out.String(), "Interpreting as a \"NO\", aborting") {
		t.Errorf("remove declined: %d %q", code, te.out.String())
	}
	if code := te.run("remove", "0", "-y"); code != 0 || te.out.String() != "Removed model 0\n" {
		t.Errorf("remove: %d %q", code, te.out.String())
	}
	if code := te.run("remove", "1", "-y"); code != 0 || te.out.String() != "Removed last model, howdy disabled for user\n" {
		t.Errorf("remove last: %d %q", code, te.out.String())
	}
	if code := te.run("clear", "-y"); code != 1 || te.out.String() != "alice has no models or they have been cleared already\n" {
		t.Errorf("clear nothing: %d %q", code, te.out.String())
	}
}

func TestAddRefusals(t *testing.T) {
	te := setup(t)
	te.frames = []*imgproc.Gray{litFrame()}

	te.faces = 2
	if code := te.run("add", "-y"); code != 1 || !strings.Contains(te.out.String(), "Multiple faces detected, aborting\n") {
		t.Errorf("two faces: %d %q", code, te.out.String())
	}
	te.faces = 0
	if code := te.run("add", "-y"); code != 1 || !strings.HasSuffix(te.out.String(), "No face detected, aborting\n") {
		t.Errorf("no face: %d %q", code, te.out.String())
	}
	te.frames = []*imgproc.Gray{imgproc.NewGray(64, 36)}
	if code := te.run("add", "-y"); code != 1 || !strings.HasSuffix(te.out.String(), "Camera saw only black frames - is IR emitter working?\n") {
		t.Errorf("black: %d %q", code, te.out.String())
	}
	if _, err := os.Stat(filepath.Join(te.Paths.UserModelsDir, "alice.dat")); err == nil {
		t.Error("a failed add wrote a model")
	}
}

func TestPythonReadsOurModel(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	te := setup(t)
	te.frames = []*imgproc.Gray{litFrame()}
	te.faces = 1
	if code := te.run("add", "x", "-y"); code != 0 {
		t.Fatal(te.out.String())
	}
	p := filepath.Join(te.Paths.UserModelsDir, "alice.dat")
	data, _ := os.ReadFile(p)
	// What Howdy's list.py and compare.py do with it
	out, err := exec.Command(py, "-I", "-c", `
import json, sys
m = json.load(open(sys.argv[1]))
enc = []
for x in m: enc += x["data"]
assert len(enc) == 1 and len(enc[0]) == 128
print(str(m[0]["id"]) + "," + m[0]["label"], end="")
sys.stderr.write(json.dumps(m))`, p).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !strings.HasPrefix(string(out), "0,x") || !strings.HasSuffix(string(out), string(data)) {
		t.Errorf("python read it differently: %s", out)
	}
	if _, err := model.Parse(data); err != nil {
		t.Error(err)
	}
}

func TestDisableSet(t *testing.T) {
	te := setup(t)
	if code := te.run("disable", "1"); code != 0 || te.out.String() != "Howdy has been disabled\n" {
		t.Errorf("disable: %d %q", code, te.out.String())
	}
	if code := te.run("disable", "true"); code != 1 || te.out.String() != "The disable option has already been set to true\n" {
		t.Errorf("disable again: %d %q", code, te.out.String())
	}
	if code := te.run("disable", "maybe"); code != 1 {
		t.Errorf("bad disable value: %d", code)
	}
	if code := te.run("set", "certainty", "3"); code != 0 || te.out.String() != "Config option updated\n" {
		t.Errorf("set: %d %q", code, te.out.String())
	}
	if code := te.run("set", "nope", "3"); code != 1 || te.out.String() != "Could not find a \"nope\" config option to set\n" {
		t.Errorf("set missing: %d %q", code, te.out.String())
	}
	if code := te.run("set", "certainty", "3 ; x"); code != 1 {
		t.Errorf("set to an ambiguous value: %d", code)
	}
	data, _ := os.ReadFile(te.Paths.ConfigFile())
	if !strings.Contains(string(data), "disabled = true\n") || !strings.Contains(string(data), "certainty = 3\n") {
		t.Errorf("config now:\n%s", data)
	}
	st, _ := os.Stat(te.Paths.ConfigFile())
	if st.Mode().Perm() != 0o644 {
		t.Errorf("config mode changed to %v", st.Mode())
	}
}

func TestConfigEditor(t *testing.T) {
	te := setup(t)
	if code := te.run("config"); code != 0 || te.out.String() != "Opening config.ini in nano\n" {
		t.Errorf("config: %d %q", code, te.out.String())
	}
	if len(te.edited) != 2 || te.edited[0] != "/usr/bin/nano" || te.edited[1] != te.Paths.ConfigFile() {
		t.Errorf("editor argv %v", te.edited)
	}
}

func TestSnapshot(t *testing.T) {
	te := setup(t)
	te.frames = []*imgproc.Gray{litFrame()}
	if code := te.run("snapshot"); code != 0 || !strings.HasPrefix(te.out.String(), "Generated snapshot saved as\n") {
		t.Fatalf("snapshot: %d %q", code, te.out.String())
	}
	path := strings.TrimSpace(strings.TrimPrefix(te.out.String(), "Generated snapshot saved as\n"))
	if filepath.Dir(path) != te.Paths.SnapshotsDir() {
		t.Errorf("saved to %s", path)
	}
}

func TestVersion(t *testing.T) {
	te := setup(t)
	if code := te.run("version"); code != 0 || !strings.HasPrefix(te.out.String(), "Howdy 3.0.0 BETA\n") {
		t.Errorf("version: %d %q", code, te.out.String())
	}
}

func TestTest(t *testing.T) {
	te := setup(t)
	te.frames = []*imgproc.Gray{imgproc.NewGray(64, 36), litFrame()}
	te.faces = 0
	te.stopAfter = 4

	if code := te.run("test"); code != 0 {
		t.Fatalf("test without a model: exit %d\n%s%s", code, te.out.String(), te.errOut.String())
	}
	for _, want := range []string{
		"No face model known for alice, only showing detection\n",
		"Testing the camera at " + filepath.Join(te.dir, "video0") + " for alice\n",
		"Certainty 3.5, dark threshold 60, upsample 1\n",
		"black frame\n",
		"no face\n",
		"\n4 frames in 0.3 s, 14.3 fps\n2 dark or black, 0 with a face\n",
	} {
		if !strings.Contains(te.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, te.out.String())
		}
	}

	te.frames = []*imgproc.Gray{litFrame()}
	te.faces = 1
	te.stopAfter = 0
	if code := te.run("add", "desk", "-y"); code != 0 {
		t.Fatalf("add: exit %d", code)
	}
	te.frames = []*imgproc.Gray{imgproc.NewGray(64, 36), litFrame()}
	te.stopAfter = 6
	if code := te.run("test"); code != 0 {
		t.Fatalf("test: exit %d\n%s%s", code, te.out.String(), te.errOut.String())
	}
	out := te.out.String()
	if strings.Contains(out, "No face model") || !strings.Contains(out, "\n3 dark or black, 3 with a face\n") ||
		!strings.Contains(out, " of 3 faces under 3.5\n") {
		t.Errorf("test:\n%s", out)
	}
	// Every face line ends in a certainty and either the label or "no match"
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) == 7 && f[3] == "1" && f[6] != "desk" && f[6] != "match" {
			t.Errorf("face line %q", l)
		}
	}
}

func TestPin(t *testing.T) {
	te := setup(t)
	pinFile := filepath.Join(te.dir, "pin", "alice")

	if code := te.run("pin"); code != 1 || !strings.Contains(te.out.String(), "howdy pin set") {
		t.Errorf("pin without an action: exit %d\n%s", code, te.out.String())
	}
	te.run("pin", "status")
	if !strings.Contains(te.out.String(), "PIN: alice has none") {
		t.Errorf("status: %s", te.out.String())
	}

	// Refused: wrong length, not digits, easy to guess, two different entries.
	// None of them may leave a file behind.
	for _, secrets := range [][]string{
		{"123"}, {"12345"}, {"abcd"}, {"1111"}, {"1234"}, {"4321"}, {"2121"}, {"4821", "4822"},
	} {
		te.secrets = secrets
		if code := te.run("pin", "set"); code != 1 {
			t.Errorf("%v accepted, exit %d", secrets, code)
		}
		if _, err := os.Stat(pinFile); err == nil {
			t.Fatalf("%v left a PIN file", secrets)
		}
	}

	te.secrets = []string{"4821", "4821"}
	if code := te.run("pin", "set"); code != 0 {
		t.Fatalf("set: exit %d\n%s%s", code, te.out.String(), te.errOut.String())
	}
	if strings.Contains(te.out.String()+te.errOut.String(), "4821") {
		t.Error("the PIN was printed")
	}
	if !strings.Contains(te.out.String(), "pam_howdy.so pin") {
		t.Errorf("no hint about the PAM argument:\n%s", te.out.String())
	}
	if st, err := os.Stat(pinFile); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("pin file: %v %v", st, err)
	}
	hash, err := pin.Load(te.Paths.PinDir, "alice", os.Geteuid())
	if err != nil || !pin.Verify([]byte("4821"), hash) || pin.Verify([]byte("4822"), hash) {
		t.Errorf("stored hash doesn't check: %v", err)
	}

	te.run("pin", "status")
	if !strings.Contains(te.out.String(), "PIN: alice has one") {
		t.Errorf("status: %s", te.out.String())
	}

	// The wrong tries the PAM module counts, and what resets them
	counter := pinFile + ".fail"
	os.WriteFile(counter, []byte("2\n"), 0o600)
	te.run("pin", "status")
	if !strings.Contains(te.out.String(), "2 of 6 wrong tries") {
		t.Errorf("status with 2 wrong tries: %s", te.out.String())
	}
	os.WriteFile(counter, []byte("6\n"), 0o600)
	te.run("pin", "status")
	if !strings.Contains(te.out.String(), "switched off") {
		t.Errorf("status when locked: %s", te.out.String())
	}
	te.secrets = []string{"4821", "4821"}
	if code := te.run("pin", "set"); code != 0 {
		t.Fatalf("setting it again: exit %d", code)
	}
	if _, err := os.Stat(counter); err == nil {
		t.Error("setting a PIN again left the switched off count behind")
	}
	os.WriteFile(counter, []byte("3\n"), 0o600)
	if code := te.run("pin", "clear"); code != 0 {
		t.Errorf("clear: exit %d", code)
	}
	if _, err := os.Stat(counter); err == nil {
		t.Error("clearing left the count behind")
	}
	te.secrets = []string{"4821", "4821"}
	te.run("pin", "set")
	if code := te.run("pin", "clear"); code != 0 {
		t.Errorf("clear: exit %d", code)
	}
	if _, err := os.Stat(pinFile); err == nil {
		t.Error("PIN still there after clear")
	}
	if code := te.run("pin", "clear"); code != 1 {
		t.Errorf("clearing twice: exit %d", code)
	}
}

func TestPinLockScreen(t *testing.T) {
	te := setup(t)
	userFile := filepath.Join(te.dir, "pin-user", "alice")
	rootFile := filepath.Join(te.dir, "pin", "alice")

	te.secrets = []string{"4821", "4821"}
	if code := te.run("pin", "set", "lock-screen"); code != 0 {
		t.Fatalf("set lock-screen: exit %d\n%s%s", code, te.out.String(), te.errOut.String())
	}
	if !strings.Contains(te.out.String(), "pam_howdy.so pin-user") {
		t.Errorf("no hint about the pin-user argument:\n%s", te.out.String())
	}
	for _, f := range []string{userFile, userFile + ".fail"} {
		if st, err := os.Stat(f); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, st, err)
		}
	}
	if st, _ := os.Stat(filepath.Dir(userFile)); st.Mode().Perm() != 0o755 {
		t.Errorf("user PIN dir mode %v", st.Mode().Perm())
	}
	if _, err := os.Stat(rootFile); err == nil {
		t.Error("the lock screen PIN also made a root PIN")
	}
	if c, _ := os.ReadFile(userFile + ".fail"); string(c) != "0\n" {
		t.Errorf("counter %q, the module can't create it so it must exist", c)
	}
	hash, err := pin.Load(te.Paths.PinUserDir, "alice", os.Geteuid())
	if err != nil || !pin.Verify([]byte("4821"), hash) {
		t.Errorf("stored hash: %v", err)
	}

	// Locked, then set again: the counter is rewritten, not removed
	os.WriteFile(userFile+".fail", []byte("6\n"), 0o600)
	te.run("pin", "status")
	if !strings.Contains(te.out.String(), "lock screen PIN: alice has one") || !strings.Contains(te.out.String(), "switched off") {
		t.Errorf("status:\n%s", te.out.String())
	}
	te.secrets = []string{"9264", "9264"}
	if code := te.run("pin", "set", "lock-screen"); code != 0 {
		t.Fatalf("set again: exit %d", code)
	}
	if c, _ := os.ReadFile(userFile + ".fail"); string(c) != "0\n" {
		t.Errorf("counter after setting again: %q", c)
	}

	// The root PIN is separate, and clearing one leaves the other
	te.secrets = []string{"4821", "4821"}
	if code := te.run("pin", "set"); code != 0 {
		t.Fatal("set")
	}
	if code := te.run("pin", "clear", "lock-screen"); code != 0 {
		t.Errorf("clear lock-screen: exit %d", code)
	}
	for _, f := range []string{userFile, userFile + ".fail"} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s left after clearing", f)
		}
	}
	if _, err := os.Stat(rootFile); err != nil {
		t.Error("clearing the lock screen PIN removed the root one")
	}
	for _, args := range [][]string{{"pin", "set", "bogus"}, {"pin", "clear", "x", "y"}, {"pin", "status", "lock-screen"}} {
		if code := te.run(args...); code != 1 {
			t.Errorf("%v: exit %d, want the usage text", args, code)
		}
	}
}

func TestColourCameraWarning(t *testing.T) {
	te := setup(t)
	te.frames = []*imgproc.Gray{litFrame()}
	te.stopAfter = 5
	if code := te.run("test"); code != 0 || strings.Contains(te.errOut.String(), "colour camera") {
		t.Fatalf("IR camera: exit %d, stderr %q", code, te.errOut.String())
	}

	te.colour = true
	te.stopAfter = 5
	te.run("test")
	if n := strings.Count(te.errOut.String(), "regular colour camera"); n != 1 {
		t.Errorf("warned %d times over several frames, want once:\n%s", n, te.errOut.String())
	}
	if !strings.Contains(te.errOut.String(), "required_matches = 2") {
		t.Errorf("warning says nothing to do:\n%s", te.errOut.String())
	}
}

// With recognizer = sface, add writes a dlib model to the .dat file and an
// sface one under the same id, and remove and clear take both away
func TestAddSFace(t *testing.T) {
	te := setup(t)
	te.frames = []*imgproc.Gray{litFrame()}
	te.faces = 1
	cfg, _ := os.ReadFile(te.Paths.ConfigFile())
	cfg = bytes.Replace(cfg, []byte("[core]\n"), []byte("[core]\nrecognizer = sface\n"), 1)
	os.WriteFile(te.Paths.ConfigFile(), cfg, 0o644)

	if code := te.run("add", "-y"); code != 1 || !strings.Contains(te.out.String(), "The sface models are missing") {
		t.Fatalf("add without the ONNX files: exit %d %q", code, te.out.String())
	}
	os.Mkdir(te.Paths.ONNXDir, 0o755)
	os.WriteFile(filepath.Join(te.Paths.ONNXDir, spec.YuNetFile), nil, 0o644)
	os.WriteFile(filepath.Join(te.Paths.ONNXDir, spec.SFaceFile), nil, 0o644)

	te.loaded = nil
	for i := 0; i < 2; i++ {
		if code := te.run("add", "-y"); code != 0 {
			t.Fatalf("add: exit %d\n%s%s", code, te.out.String(), te.errOut.String())
		}
	}
	if strings.Join(te.loaded, " ") != "dlib sface dlib sface" {
		t.Errorf("loaded %v", te.loaded)
	}
	if !strings.Contains(te.out.String(), "sface: used 5 of 5 frames, least similar ") {
		t.Errorf("add output:\n%s", te.out.String())
	}
	dat, err := model.Load(te.Paths.UserModelsDir, "alice", os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	sf, err := model.LoadSize(te.Paths.SFaceModelsDir, "alice", os.Getuid(), spec.Size)
	if err != nil {
		t.Fatal(err)
	}
	if encs, owner := sf.Enrolled(dat, spec.Encoder); len(encs) != 2 || owner[0] != 0 || owner[1] != 1 {
		t.Fatalf("sface models %v %v", encs, owner)
	}
	var norm float64
	for _, v := range sf.Entries[0].Values["data"].([]model.Value)[0].([]model.Value) {
		norm += v.(float64) * v.(float64)
	}
	if math.Abs(norm-1) > 1e-9 {
		t.Errorf("stored sface descriptor has length^2 %v", norm)
	}
	st, err := os.Stat(filepath.Join(te.Paths.SFaceModelsDir, "alice.dat"))
	if err != nil || st.Mode().Perm() != 0o640 {
		t.Errorf("sface file %v %v", st, err)
	}

	if code := te.run("remove", "0", "-y"); code != 0 {
		t.Fatalf("remove: %d %q", code, te.out.String())
	}
	sf, _ = model.LoadSize(te.Paths.SFaceModelsDir, "alice", os.Getuid(), spec.Size)
	if len(sf.Entries) != 1 {
		t.Errorf("%d sface entries after removing one of two", len(sf.Entries))
	}
	if code := te.run("clear", "-y"); code != 0 {
		t.Fatalf("clear: %d %q", code, te.out.String())
	}
	if _, err := os.Stat(filepath.Join(te.Paths.SFaceModelsDir, "alice.dat")); !os.IsNotExist(err) {
		t.Errorf("sface file left after clear: %v", err)
	}
}

// A model removed from the .dat file by something other than howdy-go
// (howdy-gtk, Python Howdy) leaves its sface descriptor behind. When a dlib
// add later reuses the id, that descriptor must not come back.
func TestSFaceOrphanNotRevived(t *testing.T) {
	te := setup(t)
	te.frames = []*imgproc.Gray{litFrame()}
	te.faces = 1
	os.Mkdir(te.Paths.ONNXDir, 0o755)
	os.WriteFile(filepath.Join(te.Paths.ONNXDir, spec.YuNetFile), nil, 0o644)
	os.WriteFile(filepath.Join(te.Paths.ONNXDir, spec.SFaceFile), nil, 0o644)
	base, _ := os.ReadFile(te.Paths.ConfigFile())
	use := func(name string) {
		cfg := bytes.Replace(base, []byte("[core]\n"), []byte("[core]\nrecognizer = "+name+"\n"), 1)
		os.WriteFile(te.Paths.ConfigFile(), cfg, 0o644)
	}

	use("sface")
	for i := 0; i < 2; i++ {
		if code := te.run("add", "-y"); code != 0 {
			t.Fatalf("add: exit %d\n%s", code, te.out.String())
		}
	}
	// Model 1 removed from the .dat file only
	dat, _ := model.Load(te.Paths.UserModelsDir, "alice", os.Getuid())
	dat.Entries = dat.Entries[:1]
	if err := model.Save(te.Paths.UserModelsDir, "alice", os.Getuid(), dat); err != nil {
		t.Fatal(err)
	}
	te.clock = te.clock.Add(time.Hour)
	use("dlib")
	if code := te.run("add", "-y"); code != 0 {
		t.Fatalf("dlib add: exit %d\n%s", code, te.out.String())
	}

	dat, _ = model.Load(te.Paths.UserModelsDir, "alice", os.Getuid())
	if id, _ := model.ID(dat.Entries[1]); id != 1 {
		t.Fatalf("the dlib add got id %d, this test needs it to reuse 1", id)
	}
	s := &config.Settings{Recognizer: "sface"}
	known, owner, err := backend.Known(te.Paths, s, "alice", dat)
	if err != nil || len(known) != 1 || owner[0] != 0 {
		t.Errorf("sface matches %d descriptors, owners %v, err %v; want only model 0's", len(known), owner, err)
	}
}
