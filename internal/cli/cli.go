package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/fabcj/howdy-go/internal/backend"
	"github.com/fabcj/howdy-go/internal/compare"
	"github.com/fabcj/howdy-go/internal/config"
	"github.com/fabcj/howdy-go/internal/enroll"
	"github.com/fabcj/howdy-go/internal/face"
	"github.com/fabcj/howdy-go/internal/imgproc"
	"github.com/fabcj/howdy-go/internal/model"
	"github.com/fabcj/howdy-go/internal/paths"
	"github.com/fabcj/howdy-go/internal/pyfmt"
	"github.com/fabcj/howdy-go/internal/snapshot"
)

// Version of howdy-go, set by the PKGBUILD
var Version = "dev"

// Env is everything the CLI touches outside itself, so tests can fake it
type Env struct {
	Args   []string // without the program name
	Getenv func(string) string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Euid   int

	// Username for a uid, and the name of the user running this
	UserName    func(uid string) string
	CurrentUser func() string
	// Primary group of a user, for the model file
	UserGID func(name string) (int, error)
	UserUID func(name string) (int, error)

	// Opens the camera from the settings, with a function to close it
	Camera func(s *config.Settings) (compare.Camera, func(), error)
	// Loads the face models
	// Faces loads a recogniser, "dlib" or "sface"
	Faces func(s *config.Settings, name string) (face.Recognizer, error)
	// Asks for a secret without showing it, for howdy pin
	ReadHidden func(prompt string) (string, error)
	// Runs the editor with the config file, as argv
	Edit     func(argv []string) error
	LookPath func(string) (string, error)

	Now   func() time.Time
	Sleep func(time.Duration)

	// Starts catching ctrl+C for howdy test, and stops again. Only test
	// catches it, so ctrl+C still kills everything else
	Interrupts func() (<-chan os.Signal, func())
}

type cmd struct {
	*Env
	a    *args
	in   *bufio.Reader
	user string

	warnedColour bool
}

// say is Python's print(), so "x\n" gives a blank line after x as it does there
func (c *cmd) say(a ...any)                  { fmt.Fprintln(c.Stdout, a...) }
func (c *cmd) printf(f string, a ...any)     { fmt.Fprintf(c.Stdout, f, a...) }
func (c *cmd) fail(f string, a ...any) int   { fmt.Fprintf(c.Stdout, f+"\n", a...); return 1 }
func (c *cmd) errorf(f string, a ...any) int { fmt.Fprintf(c.Stderr, "howdy: "+f+"\n", a...); return 1 }

// Main runs the CLI and returns the exit code
func Main(e *Env) int {
	// The original user, not root, when run through sudo and the like
	name := ""
	for _, v := range []string{e.Getenv("SUDO_USER"), e.Getenv("DOAS_USER")} {
		if v != "" {
			name = v
			break
		}
	}
	if name == "" {
		if uid := e.Getenv("PKEXEC_UID"); uid != "" {
			name = e.UserName(uid)
		}
	}
	if name == "" {
		for _, k := range []string{"LOGNAME", "USER", "LNAME", "USERNAME"} {
			if v := e.Getenv(k); v != "" {
				name = v
				break
			}
		}
	}
	if name == "" {
		name = e.CurrentUser()
	}
	if name == "" {
		fmt.Fprintln(e.Stdout, "Could not determine user, please use the --user flag")
		return 1
	}

	if len(e.Args) == 0 {
		fmt.Fprintf(e.Stdout, "current active user: %s\n\n", name)
		fmt.Fprint(e.Stdout, help)
		return 0
	}

	a, err := parse(e.Args, name)
	var ue usageError
	if errors.As(err, &ue) {
		fmt.Fprint(e.Stderr, usage)
		fmt.Fprintf(e.Stderr, "howdy: error: %s\n", ue)
		return 2
	}
	if a.help {
		fmt.Fprint(e.Stdout, help)
		return 0
	}

	if e.Euid != 0 {
		fmt.Fprint(e.Stdout, "Please run this command as root:\n\n")
		fmt.Fprintln(e.Stdout, "\tsudo howdy "+strings.Join(e.Args, " "))
		return 1
	}
	if a.user == "root" {
		fmt.Fprintln(e.Stdout, "Can't run howdy commands as root, please run this command with the --user flag")
		return 1
	}

	c := &cmd{Env: e, a: a, in: bufio.NewReader(e.Stdin), user: a.user}
	// The user ends up in a path under the models dir, and this runs as root
	if a.command != "version" && a.command != "config" && !model.ValidUser(a.user) {
		return c.errorf("%q isn't a valid username", a.user)
	}

	switch a.command {
	case "add":
		return c.add()
	case "clear":
		return c.clear()
	case "config":
		return c.config()
	case "disable":
		return c.disable()
	case "list":
		return c.list()
	case "pin":
		return c.pin()
	case "remove":
		return c.remove()
	case "set":
		return c.set()
	case "snapshot":
		return c.snapshot()
	case "test":
		return c.test()
	}
	c.say("Howdy 3.0.0 BETA")
	c.say("howdy-go " + Version)
	return 0
}

// input is Python's input(): the prompt, then one line without its newline
func (c *cmd) input(prompt string) (string, error) {
	fmt.Fprint(c.Stdout, prompt)
	line, err := c.in.ReadString('\n')
	if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
		return "", err
	}
	return strings.TrimSuffix(line, "\n"), nil
}

func (c *cmd) confirm(question string) bool {
	c.say(question)
	ans, err := c.input("Do you want to continue [y/N]: ")
	if err != nil || strings.ToLower(ans) != "y" {
		c.say("\nInterpreting as a \"NO\", aborting")
		return false
	}
	return true
}

func (c *cmd) settings() (*config.File, *config.Settings, error) {
	f, err := config.Read(paths.ConfigFile())
	if err != nil {
		return nil, nil, err
	}
	s, err := config.Load(f)
	return f, s, err
}

func dirExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (c *cmd) loadModels() (*model.Models, error) {
	return model.Load(paths.UserModelsDir, c.user, paths.ModelOwnerUID())
}

func (c *cmd) list() int {
	if !dirExists(paths.UserModelsDir) {
		c.say("Face models have not been initialized yet, please run:")
		c.printf("\n\tsudo howdy -U %s add\n\n", c.user)
		return 1
	}
	m, err := c.loadModels()
	if errors.Is(err, model.ErrNoModel) {
		if !c.a.plain {
			c.printf("No face model known for the user %s, please run:\n", c.user)
			c.printf("\n\tsudo howdy -U %s add\n\n", c.user)
		}
		return 1
	}
	if err != nil {
		return c.errorf("%v", err)
	}

	if !c.a.plain {
		c.printf("Known face models for %s:\n", c.user)
		c.say("\n\033[1;29mID  Date                 Label\033[0m")
	}
	for _, e := range m.Entries {
		id := model.Str(e.Values["id"])
		c.printf("%s", id)
		if c.a.plain {
			c.printf(",")
		} else {
			c.printf("%s", strings.Repeat(" ", max(0, 4-len(id))))
		}
		c.printf("%s", localTime(e.Values["time"]))
		if c.a.plain {
			c.printf(",")
		} else {
			c.printf("  ")
		}
		c.say(model.Label(e))
	}
	c.say()
	return 0
}

func localTime(v model.Value) string {
	var sec int64
	switch t := v.(type) {
	case model.Int:
		sec = int64(t)
	case float64:
		sec = int64(t)
	}
	return time.Unix(sec, 0).Local().Format("2006-01-02 15:04:05")
}

func (c *cmd) clear() int {
	if !dirExists(paths.UserModelsDir) {
		return c.fail("No models created yet, can't clear them if they don't exist")
	}
	p, _ := model.Path(paths.UserModelsDir, c.user)
	if st, err := os.Lstat(p); err != nil || !st.Mode().IsRegular() {
		return c.fail("%s has no models or they have been cleared already", c.user)
	}
	if !c.a.yes && !c.confirm("This will clear all models for "+c.user) {
		return 1
	}
	if err := model.Remove(paths.UserModelsDir, c.user); err != nil {
		return c.errorf("%v", err)
	}
	if err := c.removeSFace(); err != nil {
		return c.errorf("%v", err)
	}
	c.say("\nModels cleared")
	return 0
}

// removeSFace deletes the user's sface descriptors, if there are any. A
// leftover file couldn't match anyway: compare only uses descriptors whose
// model the .dat file still has.
func (c *cmd) removeSFace() error {
	err := model.Remove(paths.SFaceModelsDir, c.user)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// pruneSFace removes the sface descriptors whose model the .dat file (dat)
// no longer has, if there are any
func (c *cmd) pruneSFace(dat *model.Models) error {
	m, err := model.LoadSize(paths.SFaceModelsDir, c.user, paths.ModelOwnerUID(), backend.SFaceSize)
	if errors.Is(err, model.ErrNoModel) {
		return nil
	}
	if err != nil {
		return err
	}
	m.Prune(dat)
	if len(m.Entries) == 0 {
		return c.removeSFace()
	}
	gid, err := c.UserGID(c.user)
	if err != nil {
		return err
	}
	return model.Save(paths.SFaceModelsDir, c.user, gid, m)
}

func (c *cmd) remove() int {
	if len(c.a.arguments) == 0 {
		c.say("Please add the ID of the model you want to remove as an argument")
		c.say("For example:")
		c.say("\n\thowdy remove 0\n")
		c.say("You can find the IDs by running:")
		c.say("\n\thowdy list\n")
		return 1
	}
	if !dirExists(paths.UserModelsDir) {
		c.say("Face models have not been initialized yet, please run:")
		c.say("\n\thowdy add\n")
		return 1
	}
	m, err := c.loadModels()
	if errors.Is(err, model.ErrNoModel) {
		c.printf("No face model known for the user %s, please run:\n", c.user)
		c.say("\n\thowdy add\n")
		return 1
	}
	if err != nil {
		return c.errorf("%v", err)
	}

	id := c.a.arguments[0]
	found := -1
	for i, e := range m.Entries {
		if model.Str(e.Values["id"]) == id {
			found = i
			break
		}
	}
	if found >= 0 && !c.a.yes {
		if !c.confirm(fmt.Sprintf("This will remove the model called \"%s\" for %s", model.Label(m.Entries[found]), c.user)) {
			return 1
		}
		c.say()
	}
	if found < 0 {
		return c.fail("No model with ID %s exists for %s", id, c.user)
	}

	if len(m.Entries) == 1 {
		if err := model.Remove(paths.UserModelsDir, c.user); err != nil {
			return c.errorf("%v", err)
		}
		if err := c.removeSFace(); err != nil {
			return c.errorf("%v", err)
		}
		c.say("Removed last model, howdy disabled for user")
		return 0
	}
	var rest []*model.Object
	for _, e := range m.Entries {
		if model.Str(e.Values["id"]) != id {
			rest = append(rest, e)
		}
	}
	m.Entries = rest
	if err := c.save(m); err != nil {
		return c.errorf("%v", err)
	}
	if err := c.pruneSFace(m); err != nil {
		return c.errorf("%v", err)
	}
	c.printf("Removed model %s\n", id)
	return 0
}

func (c *cmd) save(m *model.Models) error {
	gid, err := c.UserGID(c.user)
	if err != nil {
		return err
	}
	return model.Save(paths.UserModelsDir, c.user, gid, m)
}

func (c *cmd) config() int {
	var editor, name string
	if pref := c.Getenv("EDITOR"); pref != "" {
		if p, err := c.LookPath(pref); err == nil {
			editor, name = p, filepath.Base(pref)
		}
	}
	if editor == "" {
		for _, e := range []string{"nano", "vi"} {
			if p, err := c.LookPath(e); err == nil {
				editor, name = p, filepath.Base(p)
				break
			}
		}
	}
	if editor == "" {
		c.say("Error: Could not find a suitable text editor.")
		c.say("Please install 'nano' or 'vi', or set the EDITOR environment variable.")
		c.say("If you are running this command with sudo, try 'sudo -E howdy config' to preserve your EDITOR variable.")
		return 0
	}
	c.printf("Opening config.ini in %s\n", name)
	// As an argv, never through a shell
	if err := c.Edit([]string{editor, paths.ConfigFile()}); err != nil {
		c.printf("Failed to open editor: %v\n", err)
	}
	return 0
}

func (c *cmd) disable() int {
	if len(c.a.arguments) == 0 {
		return c.fail("Please add a 0 (enable) or a 1 (disable) as an argument")
	}
	arg := c.a.arguments[0]
	var value string
	switch {
	case arg == "1" || strings.ToLower(arg) == "true":
		value = "true"
	case arg == "0" || strings.ToLower(arg) == "false":
		value = "false"
	default:
		return c.fail("Please only use 0 (enable) or 1 (disable) as an argument")
	}

	f, err := config.Read(paths.ConfigFile())
	if err != nil {
		return c.errorf("%v", err)
	}
	if cur, err := f.Raw("core", "disabled"); err == nil && cur == value {
		return c.fail("The disable option has already been set to %s", value)
	}
	data, err := f.Set("core", "disabled", value)
	if err != nil {
		return c.errorf("%v", err)
	}
	if err := writeConfig(paths.ConfigFile(), data); err != nil {
		return c.errorf("%v", err)
	}
	if value == "true" {
		c.say("Howdy has been disabled")
	} else {
		c.say("Howdy has been enabled")
	}
	return 0
}

func (c *cmd) set() int {
	if len(c.a.arguments) < 2 {
		c.say("Please add a setting you would like to change and the value to set it to")
		c.say("For example:")
		c.say("\n\thowdy set certainty 3\n")
		return 1
	}
	key, value := c.a.arguments[0], c.a.arguments[1]
	f, err := config.Read(paths.ConfigFile())
	if err != nil {
		return c.errorf("%v", err)
	}

	// Either section.key or a key that only one section has
	var section string
	if s, k, ok := strings.Cut(key, "."); ok && f.Has(s, k) {
		section, key = s, k
	} else {
		in := f.SectionsWithKey(key)
		switch len(in) {
		case 0:
			return c.fail("Could not find a \"%s\" config option to set", key)
		case 1:
			section = in[0]
		default:
			return c.errorf("%q is in more than one section, use section.%s", key, key)
		}
	}

	data, err := f.Set(section, key, value)
	if err != nil {
		return c.errorf("%v", err)
	}
	if err := writeConfig(paths.ConfigFile(), data); err != nil {
		return c.errorf("%v", err)
	}
	c.say("Config option updated")
	return 0
}

// writeConfig replaces the config file in one step, keeping its mode and
// owner
func writeConfig(path string, data []byte) error {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return &fs.PathError{Op: "lstat", Path: path, Err: err}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("%s isn't a regular file", path)
	}
	tmp := path + ".tmp"
	_ = unix.Unlink(tmp)
	fd, err := unix.Open(tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return &fs.PathError{Op: "open", Path: tmp, Err: err}
	}
	f := os.NewFile(uintptr(fd), tmp)
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = unix.Unlink(tmp)
		}
	}()
	if unix.Geteuid() == 0 {
		if err := unix.Fchown(fd, int(st.Uid), int(st.Gid)); err != nil {
			return err
		}
	}
	if err := unix.Fchmod(fd, st.Mode&0o7777); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := unix.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func (c *cmd) noDevice(s *config.Settings) int {
	if s.WarnNoDevice {
		c.say("Howdy could not find a camera device at the path specified in the config file.")
		c.say("It is very likely that the path is not configured correctly, please edit the 'device_path' config value by running:")
		c.say("\n\tsudo howdy config\n")
	}
	return compare.BadDevice
}

func (c *cmd) openCamera(s *config.Settings) (compare.Camera, func(), int) {
	if _, err := os.Stat(s.DevicePath); err != nil {
		return nil, nil, c.noDevice(s)
	}
	cam, closeCam, err := c.Camera(s)
	if err != nil {
		fmt.Fprintf(c.Stderr, "howdy: can't use the camera at %s: %v\n", s.DevicePath, err)
		return nil, nil, compare.BadDevice
	}
	return cam, closeCam, 0
}

func (c *cmd) readFrame(cam compare.Camera) (*imgproc.Gray, int) {
	g, err := cam.Read(2 * time.Second)
	if err != nil {
		c.say("Failed to read camera specified in the 'device_path' config option, aborting")
		return nil, compare.BadDevice
	}
	c.warnColour(cam)
	return g, 0
}

// colourCamera is what a camera that can tell colour from IR frames adds
type colourCamera interface{ Colour() bool }

// warnColour says once that the camera looks like a regular webcam. The
// thresholds were measured on an IR camera only, and a photo or a screen
// is far closer to a real face on a colour one.
func (c *cmd) warnColour(cam compare.Camera) {
	cc, ok := cam.(colourCamera)
	if c.warnedColour || !ok || !cc.Colour() {
		return
	}
	c.warnedColour = true
	fmt.Fprintln(c.Stderr, "howdy: this looks like a regular colour camera, not an IR one. howdy-go's thresholds were")
	fmt.Fprintln(c.Stderr, "howdy: only measured on an IR camera, and a photo or screen of you can pass far more easily")
	fmt.Fprintln(c.Stderr, "howdy: on a colour one. Set required_matches = 2, keep your password as the fallback, and")
	fmt.Fprintln(c.Stderr, "howdy: see \"Regular cameras\" in the README.")
}

func (c *cmd) snapshot() int {
	_, s, err := c.settings()
	if err != nil {
		return c.errorf("%v", err)
	}
	cam, closeCam, code := c.openCamera(s)
	if code != 0 {
		return code
	}
	defer closeCam()

	// The first frame wakes the emitter up
	if _, code := c.readFrame(cam); code != 0 {
		return code
	}
	var frames []*imgproc.Gray
	for len(frames) < 4 {
		g, code := c.readFrame(cam)
		if code != 0 {
			return code
		}
		frames = append(frames, g)
	}

	now := c.Now()
	img, err := snapshot.Build(frames, []string{
		"GENERATED SNAPSHOT",
		"Date: " + now.UTC().Format("2006/01/02 15:04:05") + " UTC",
		"Dark threshold config: " + pyfmt.Float(s.DarkThreshold),
		"Certainty config: " + pyfmt.Float(s.Certainty),
	}, snapshot.LoadLogo(paths.Logo()))
	path := ""
	if err == nil {
		path, err = snapshot.Save(paths.SnapshotsDir(), now, img)
	}
	if err != nil {
		c.say("Could not save the snapshot")
		return 0
	}
	c.say("Generated snapshot saved as")
	c.say(path)
	return 0
}

func (c *cmd) add() int {
	if _, err := os.Stat(paths.ShapePredictor()); err != nil {
		c.say("Data files have not been downloaded, please run the following commands:")
		c.say("\n\tcd " + paths.DlibDataDir)
		c.say("\tsudo ./install.sh\n")
		return 1
	}
	_, s, err := c.settings()
	if err != nil {
		return c.errorf("%v", err)
	}
	gid, err := c.UserGID(c.user)
	if err != nil {
		return c.errorf("%v", err)
	}
	// dlib always: the .dat file it writes is what list, remove, the PAM
	// module and Python Howdy go by, and recognizer = dlib has to keep working
	faces, err := c.Faces(s, "dlib")
	if err != nil {
		return c.errorf("can't load the dlib models: %v", err)
	}
	defer faces.Close()
	// And the configured recogniser too, from the same frames
	var extra face.Recognizer
	if s.Recognizer != "dlib" {
		if msg := backend.MissingData(s.Recognizer); msg != "" {
			c.say(msg)
			return 1
		}
		extra, err = c.Faces(s, s.Recognizer)
		if err != nil {
			return c.errorf("can't load the %s models: %v", s.Recognizer, err)
		}
		defer extra.Close()
	}

	if !dirExists(paths.UserModelsDir) {
		c.say("No face model folder found, creating one")
		// 0755 whatever the umask, the models loader refuses anything writable
		// by others
		if err := os.MkdirAll(paths.UserModelsDir, 0o755); err != nil {
			return c.errorf("%v", err)
		}
		if err := os.Chmod(paths.UserModelsDir, 0o755); err != nil {
			return c.errorf("%v", err)
		}
	}
	m, err := c.loadModels()
	if errors.Is(err, model.ErrNoModel) {
		m, err = &model.Models{}, nil
	}
	if err != nil {
		return c.errorf("%v", err)
	}

	if len(m.Entries) > 3 {
		c.say("NOTICE: Each additional model slows down the face recognition engine slightly")
		c.say("Press Ctrl+C to cancel\n")
	}
	if !c.a.plain {
		c.say("Adding face model for the user " + c.user)
	}

	nextID, err := m.NextID()
	if err != nil {
		return c.errorf("%v", err)
	}
	label := "Model #" + strconv.FormatInt(nextID, 10)
	if len(c.a.arguments) > 0 {
		label = c.a.arguments[0]
	}
	if c.a.yes {
		c.printf("Using default label \"%s\" because of -y flag\n", label)
	} else {
		in, err := c.input(fmt.Sprintf("Enter a label for this new model [%s]: ", label))
		if err != nil {
			return c.errorf("%v", err)
		}
		if in != "" {
			label = truncateRunes(in, 24)
		}
	}
	if strings.Contains(label, ",") {
		c.say("NOTICE: Removing illegal character \",\" from model name")
		label = strings.ReplaceAll(label, ",", "")
	}
	created := c.Now().Unix()

	cam, closeCam, code := c.openCamera(s)
	if code != 0 {
		return code
	}
	defer closeCam()

	c.say("\nPlease look straight into the camera")
	c.Sleep(2 * time.Second)

	// Throw away what the camera buffered while we waited, and give its
	// exposure a moment to settle. The first frames are often brighter or
	// stale, and a model built from one of them matches the user badly
	settle := c.Now().Add(500 * time.Millisecond)
	for c.Now().Before(settle) {
		if _, code := c.readFrame(cam); code != 0 {
			return code
		}
	}

	var encs, extraEncs [][]float64
	frames, valid, darkTries := 0, 0, 0
	var darkTotal float32
	more := func() bool {
		return len(encs) < s.EnrollFrames || extra != nil && len(extraEncs) < s.EnrollFrames
	}
	for more() && frames < 60+s.EnrollFrames*6 {
		frames++
		raw, code := c.readFrame(cam)
		if code != 0 {
			return code
		}
		gs := imgproc.CLAHE(raw, 2.0, 8, 8)
		darkness, total := imgproc.Darkness(gs)
		if total == 0 || darkness == 100 {
			continue
		}
		darkTotal += darkness
		valid++
		if float64(darkness) > s.DarkThreshold {
			darkTries++
			continue
		}

		rects, err := faces.Detect(gs)
		if err != nil {
			return c.errorf("face detection failed: %v", err)
		}
		var extraRects []face.Rect
		if extra != nil {
			if extraRects, err = extra.Detect(gs); err != nil {
				return c.errorf("face detection failed: %v", err)
			}
		}
		// Either detector seeing two faces stops it
		if len(rects) > 1 || len(extraRects) > 1 {
			closeCam()
			return c.fail("Multiple faces detected, aborting")
		}
		if len(rects) == 1 && len(encs) < s.EnrollFrames {
			enc, err := faces.Encode(raw, rects[0])
			if err != nil {
				return c.errorf("face encoding failed: %v", err)
			}
			encs = append(encs, enc.Descriptor)
		}
		if len(extraRects) == 1 && len(extraEncs) < s.EnrollFrames {
			enc, err := extra.Encode(raw, extraRects[0])
			if err != nil {
				return c.errorf("face encoding failed: %v", err)
			}
			extraEncs = append(extraEncs, enc.Descriptor)
		}
	}
	closeCam()

	if len(encs) == 0 {
		switch valid {
		case 0:
			c.say("Camera saw only black frames - is IR emitter working?")
		case darkTries:
			c.say("All frames were too dark, please check dark_threshold in config")
			c.printf("Average darkness: %s, Threshold: %s\n", pyfmt.Float32(darkTotal/float32(valid)), pyfmt.Float(s.DarkThreshold))
		default:
			c.say("No face detected, aborting")
		}
		return 1
	}
	if len(encs) < min(3, s.EnrollFrames) {
		return c.fail("Only saw a face in %d frames, please hold still and try again", len(encs))
	}
	if extra != nil && len(extraEncs) < min(3, s.EnrollFrames) {
		return c.fail("%s only saw a face in %d frames, please hold still and try again", s.Recognizer, len(extraEncs))
	}

	r, err := enroll.Average(encs)
	if err != nil {
		return c.errorf("%v", err)
	}
	if !c.a.plain {
		c.printf("\nUsed %d of %d frames, spread %.2f (worst %.2f)\n", r.Used, r.Total, r.Median, r.Worst)
	}

	m.Entries = append(m.Entries, model.NewEntry(created, label, nextID, r.Encoding))
	if err := model.Save(paths.UserModelsDir, c.user, gid, m); err != nil {
		return c.errorf("%v", err)
	}
	if extra != nil {
		if err := c.saveSFace(m, created, label, nextID, extraEncs, gid); err != nil {
			return c.errorf("the model was saved for dlib only: %v", err)
		}
	}
	c.say("\nScan complete\nAdded a new model to " + c.user)
	return 0
}

// saveSFace averages the sface descriptors and stores them under the same
// id, time and label as the dlib model, after the .dat file (dat) has it.
// Entries for models no longer in the .dat file go first.
func (c *cmd) saveSFace(dat *model.Models, created int64, label string, id int64, encs [][]float64, gid int) error {
	r, err := enroll.Average(encs)
	if err != nil {
		return err
	}
	enc := face.Unit(r.Encoding)
	if !c.a.plain {
		c.printf("sface: used %d of %d frames, least similar %.2f\n", r.Used, r.Total, face.Cosine(r.Worst/10))
	}
	if !dirExists(paths.SFaceModelsDir) {
		if err := os.MkdirAll(paths.SFaceModelsDir, 0o755); err != nil {
			return err
		}
		if err := os.Chmod(paths.SFaceModelsDir, 0o755); err != nil {
			return err
		}
	}
	m, err := model.LoadSize(paths.SFaceModelsDir, c.user, paths.ModelOwnerUID(), backend.SFaceSize)
	if errors.Is(err, model.ErrNoModel) {
		m, err = model.NewModels(backend.SFaceSize), nil
	}
	if err != nil {
		return err
	}
	m.Prune(dat)
	m.Entries = append(m.Entries, model.NewTaggedEntry(created, label, id, enc, backend.SFaceEncoder))
	return model.Save(paths.SFaceModelsDir, c.user, gid, m)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// UserGID looks a user's primary group up
func UserUID(name string) (int, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(u.Uid)
}

func UserGID(name string) (int, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(u.Gid)
}
