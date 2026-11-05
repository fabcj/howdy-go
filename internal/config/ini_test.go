package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `# Howdy config file
[core]
disabled = false
use_cnn = false
workaround = off

[video]
certainty = 4.0
timeout = 8
device_path = /dev/video0
dark_threshold = 100
max_height = 360
frame_width = 640
exposure = -1

[rubberstamps]
enabled = false
stamp_rules =
	nod		5s		failsafe     min_distance=12

[debug]
end_report = true
`

func TestSample(t *testing.T) {
	f, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if s.Certainty != 4.0 || s.Timeout != 8 || s.DevicePath != "/dev/video0" ||
		s.DarkThreshold != 100 || s.MaxHeight != 360 || s.FrameWidth != 640 ||
		s.FrameHeight != -1 || !s.EndReport || s.RequiredMatches != 1 || s.EnrollFrames != 15 {
		t.Errorf("unexpected settings %+v", s)
	}
	if _, err := f.Raw("rubberstamps", "stamp_rules"); err == nil {
		t.Error("multi-line value read as a single line")
	}
}

func TestFallbacks(t *testing.T) {
	f, err := Parse([]byte("[video]\ndevice_path = /dev/video0\n"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{Recognizer: "dlib", MatchThreshold: 0.363,
		Certainty: 3.5, RequiredMatches: 1, Timeout: 4, DevicePath: "/dev/video0",
		WarnNoDevice: true, MaxHeight: 320, FrameWidth: -1, FrameHeight: -1, DeviceFPS: -1,
		DarkThreshold: 60, DetectionUpsample: 1, EnrollFrames: 15, Exposure: -1}
	if *s != want {
		t.Errorf("got %+v\nwant %+v", *s, want)
	}
}

func TestRecognizerSettings(t *testing.T) {
	for _, c := range []struct {
		core, video string
		ok          bool
	}{
		{"recognizer = sface", "match_threshold = 0.4", true},
		{"recognizer = dlib", "", true},
		{"recognizer = SFace", "", false},
		{"recognizer = arcface", "", false},
		{"", "match_threshold = 0", false},
		{"", "match_threshold = 1", false},
		{"", "match_threshold = -0.5", false},
	} {
		f, err := Parse([]byte("[core]\n" + c.core + "\n[video]\ndevice_path = /dev/video0\n" + c.video + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Load(f); (err == nil) != c.ok {
			t.Errorf("%q %q: err %v", c.core, c.video, err)
		}
	}
}

func TestRefused(t *testing.T) {
	for name, text := range map[string]string{
		"duplicate key":     "[v]\na = 1\na = 2\n",
		"duplicate section": "[v]\na = 1\n[v]\nb = 2\n",
		"section case":      "[video]\na = 1\n[Video]\nb = 2\n",
		"no section":        "a = 1\n",
		"no delimiter":      "[v]\nfoo\n",
		"empty key":         "[v]\n= 1\n",
		"bad header":        "[a]b]\n",
		"default":           "[DEFAULT]\na = 1\n",
		"bom":               "\xef\xbb\xbf[v]\na = 1\n",
		"long line":         "[v]\na = " + strings.Repeat("9", 195) + "\n",
		"indented keys":     "[v]\n  a = 1\n  b = 2\n",
	} {
		if _, err := Parse([]byte(text)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Parse([]byte("[v]\na = " + strings.Repeat("9", 194) + "\n")); err != nil {
		t.Errorf("198 character line refused: %v", err)
	}
}

func TestValues(t *testing.T) {
	f, err := Parse([]byte("[v]\n" +
		"hash = 3.5 # x\n" +
		"semi = input ; x\n" +
		"tight = 60;x\n" +
		"pct = 50%\n" +
		"yes = YES\n" +
		"on = On\n" +
		"bad = maybe\n" +
		"hex = 0x10\n" +
		"under = 1_000\n" +
		"intf = 4.0\n" +
		"exp = 1e1\n" +
		"nan = nan\n" +
		"inf = inf\n" +
		"comma = 3,5\n" +
		"empty =\n" +
		"colon: 7\n" +
		"Upper = 2\n" +
		"plus = +4\n" +
		"crlf = 5\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	okBool := map[string]bool{"yes": true, "on": true}
	for k, want := range okBool {
		if got, err := f.Bool("v", k, false); err != nil || got != want {
			t.Errorf("Bool(%s) = %v, %v", k, got, err)
		}
	}
	okInt := map[string]int{"colon": 7, "upper": 2, "plus": 4, "crlf": 5}
	for k, want := range okInt {
		if got, err := f.Int("v", k, -1); err != nil || got != want {
			t.Errorf("Int(%s) = %v, %v", k, got, err)
		}
	}
	if got, err := f.Float("v", "exp", -1); err != nil || got != 10 {
		t.Errorf("Float(exp) = %v, %v", got, err)
	}
	if got, err := f.String("v", "tight", ""); err != nil || got != "60;x" {
		t.Errorf("String(tight) = %q, %v", got, err)
	}
	for _, k := range []string{"hash", "semi", "pct", "hex", "under", "intf", "comma", "empty"} {
		if _, err := f.Int("v", k, -1); err == nil {
			t.Errorf("Int(%s) accepted", k)
		}
	}
	for _, k := range []string{"hash", "semi", "nan", "inf", "comma", "empty"} {
		if _, err := f.Float("v", k, -1); err == nil {
			t.Errorf("Float(%s) accepted", k)
		}
	}
	if _, err := f.Bool("v", "bad", false); err == nil {
		t.Error("Bool(bad) accepted")
	}
	if got, err := f.Int("v", "missing", 9); err != nil || got != 9 {
		t.Errorf("fallback = %v, %v", got, err)
	}
}

func TestSet(t *testing.T) {
	f, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Set("core", "disabled", "true")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(sample, "disabled = false", "disabled = true", 1)
	if string(out) != want {
		t.Errorf("Set changed more than one line:\n%s", out)
	}

	out, err = f.Set("video", "required_matches", "2")
	if err != nil {
		t.Fatal(err)
	}
	g, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := g.Int("video", "required_matches", 1); n != 2 {
		t.Errorf("added key reads back as %d", n)
	}
	if !strings.Contains(string(out), "exposure = -1\nrequired_matches = 2\n") {
		t.Errorf("added key in the wrong place:\n%s", out)
	}

	// The continuation lines go with the value they belong to
	out, err = f.Set("rubberstamps", "stamp_rules", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "nod") {
		t.Error("continuation line left behind")
	}

	for _, bad := range []string{"1\n[core]", "a ; b", "50%"} {
		if _, err := f.Set("video", "certainty", bad); err == nil {
			t.Errorf("Set accepted %q", bad)
		}
	}
	if _, err := f.Set("nope", "x", "1"); err == nil {
		t.Error("Set into a missing section")
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(sample))
	f.Add([]byte("[v]\na = 1\n  b\n; c\n[w]\nd: e\r\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := Parse(data)
		if err != nil {
			return
		}
		for _, sec := range file.Sections() {
			for _, key := range file.sections[sec].keys {
				v, err := file.Raw(sec, key)
				if err != nil {
					continue
				}
				// Rewriting a value to itself must give a file that reads the same
				out, err := file.Set(sec, key, v)
				if err != nil {
					continue
				}
				again, err := Parse(out)
				if err != nil {
					t.Fatalf("Set produced an unreadable file: %v", err)
				}
				if v2, err := again.Raw(sec, key); err != nil || v2 != v {
					t.Fatalf("[%s] %s: %q became %q (%v)", sec, key, v, v2, err)
				}
			}
		}
	})
}

// Every file this parser accepts must read the same in Python's
// configparser. Skipped without python3.
func TestAgainstConfigparser(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	corpus := []string{
		sample,
		"[v]\na = 1\nb: two words\n\n# c\nc = x # not a comment\nd = 60;x\n",
		"[v]\nk =\n  one\n  two\n[w]\nUPPER = 1\n",
		"[core]\ndisabled = TRUE\n[video]\ncertainty=3.5\n timeout_like = 4\n",
	}
	dir := t.TempDir()
	var paths []string
	for i, text := range corpus {
		p := filepath.Join(dir, "c"+string(rune('0'+i))+".ini")
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	script := `
import configparser, json, sys
out = []
for p in sys.argv[1:]:
    c = configparser.ConfigParser(interpolation=None)
    c.read(p)
    out.append({s: dict(c[s]) for s in c.sections()})
print(json.dumps(out))
`
	cmd := exec.Command(py, "-I", "-c", script)
	cmd.Args = append(cmd.Args, paths...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	var want []map[string]map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &want); err != nil {
		t.Fatal(err)
	}

	for i, text := range corpus {
		f, err := Parse([]byte(text))
		if err != nil {
			t.Errorf("corpus %d refused: %v", i, err)
			continue
		}
		for sec, keys := range want[i] {
			for key, pyVal := range keys {
				got, err := f.Raw(sec, key)
				if strings.Contains(pyVal, "\n") {
					continue
				}
				if err != nil && !errors.Is(err, ErrMissing) {
					continue
				}
				if got != pyVal {
					t.Errorf("corpus %d [%s] %s: go %q, python %q", i, sec, key, got, pyVal)
				}
			}
		}
	}
}
