// howdy-compare checks the face in front of the camera against a user's
// face models. The PAM module runs it as `howdy-compare <user>` and reads
// the result from the exit code.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/fabcj/howdy-go/internal/backend"
	"github.com/fabcj/howdy-go/internal/backend/loader"
	"github.com/fabcj/howdy-go/internal/camera"
	"github.com/fabcj/howdy-go/internal/compare"
	"github.com/fabcj/howdy-go/internal/config"
	"github.com/fabcj/howdy-go/internal/harden"
	"github.com/fabcj/howdy-go/internal/imgproc"
	"github.com/fabcj/howdy-go/internal/model"
	"github.com/fabcj/howdy-go/internal/paths"
	"github.com/fabcj/howdy-go/internal/snapshot"
	"github.com/fabcj/howdy-go/internal/username"
)

// Generous caps that still stop a runaway. The CNN detector at upsample 1
// maps about 2 GB more on a 611x480 frame, HOG under 0.5 GB
const (
	addressBudget = 4 << 30
	cpuSlack      = 30
)

func main() {
	os.Exit(run())
}

func fail(code int, format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "howdy-compare: "+format+"\n", args...)
	return code
}

func run() int {
	start := time.Now()
	if err := harden.CheckNoNewPrivs(); err != nil {
		return fail(compare.Abort, "%v", err)
	}

	if len(os.Args) < 2 {
		return compare.Abort
	}
	user := os.Args[1]
	if !username.Valid(user) {
		return fail(compare.Abort, "invalid username")
	}

	// The model comes first, like compare.py, so a missing one is always 10
	models, err := model.Load(paths.UserModelsDir, user, paths.ModelOwnerUID())
	if errors.Is(err, model.ErrNoModel) {
		return compare.NoFaceModel
	}
	if err != nil {
		return fail(compare.Abort, "can't read the face model: %v", err)
	}
	// howdy add writes a dlib encoding with every model, whatever the
	// recognizer, so none here means nothing is enrolled
	if flat, _ := models.Flat(); len(flat) == 0 {
		return compare.NoFaceModel
	}
	labels := make([]string, len(models.Entries))
	for i, e := range models.Entries {
		labels[i] = model.Label(e)
	}

	file, err := config.Read(paths.ConfigFile())
	if err != nil {
		return fail(compare.Abort, "%v", err)
	}
	s, err := config.Load(file)
	if err != nil {
		return fail(compare.Abort, "%v", err)
	}
	if err := harden.Limits(addressBudget, uint64(max(s.Timeout, 0)+cpuSlack)); err != nil {
		return fail(compare.Abort, "%v", err)
	}

	// Rubberstamps aren't ported yet. Fail rather than skip the extra checks
	if s.Rubberstamps {
		return fail(compare.Abort, "rubberstamps are enabled but not supported yet")
	}

	// With sface, only its descriptors for models the .dat file still lists.
	// None means the password, not falling back to dlib
	known, owner, err := backend.Known(s, user, models)
	if errors.Is(err, backend.ErrNotEnrolled) {
		return fail(compare.NoFaceModel, "%v", err)
	}
	if err != nil {
		return fail(compare.Abort, "can't read the face model: %v", err)
	}
	if len(known) == 0 {
		return compare.NoFaceModel
	}

	if _, err := os.Stat(s.DevicePath); err != nil {
		if s.WarnNoDevice {
			fmt.Print(backend.NoDeviceText, "\n")
		}
		return compare.BadDevice
	}

	if msg := backend.MissingData(s.Recognizer); msg != "" {
		fmt.Println(msg)
		return compare.Abort
	}

	startup := time.Since(start)

	// Open the camera while the models load
	type camResult struct {
		cam  *camera.Camera
		err  error
		took time.Duration
	}
	camDone := make(chan camResult, 1)
	go func() {
		t := time.Now()
		cam, err := camera.Open(s)
		camDone <- camResult{cam, err, time.Since(t)}
	}()

	t := time.Now()
	faces, faceErr := loader.Load(s, s.Recognizer)
	modelsTook := time.Since(t)
	cr := <-camDone

	if cr.err != nil {
		if faces != nil {
			faces.Close()
		}
		return fail(compare.BadDevice, "can't use the camera at %s: %v", s.DevicePath, cr.err)
	}
	defer cr.cam.Close()
	if faceErr != nil {
		return fail(compare.Abort, "can't load the %s models: %v", s.Recognizer, faceErr)
	}
	defer faces.Close()

	snapshots := s.SaveFailed || s.SaveSuccessful
	if unix.Geteuid() == 0 {
		// Started by sudo and the like: nothing past this point needs root.
		// Writing a snapshot would, so those are off here
		uid, gid, err := harden.Nobody()
		if err != nil {
			return fail(compare.Abort, "%v", err)
		}
		if err := harden.Drop(uid, gid); err != nil {
			return fail(compare.Abort, "dropping root failed: %v", err)
		}
		if snapshots {
			fmt.Fprintln(os.Stderr, "howdy-compare: snapshots aren't saved when started as root")
			snapshots = false
		}
	}

	var uts unix.Utsname
	_ = unix.Uname(&uts)

	loop := &compare.Loop{
		Settings: s,
		Known:    known,
		Owner:    owner,
		Labels:   labels,
		Camera:   cr.cam,
		Faces:    faces,
		Match:    backend.MatcherFor(s),
		Out:      os.Stdout,
		Hostname: unix.ByteSliceToString(uts.Nodename[:]),
		Timings:  compare.Timings{Start: start, Startup: startup, Camera: cr.took, Models: modelsTook},
	}
	if snapshots {
		logo := snapshot.LoadLogo(paths.Logo())
		loop.Snapshot = func(frames []*imgproc.Gray, lines []string) {
			img, err := snapshot.Build(frames, lines, logo)
			if err != nil {
				return
			}
			_, _ = snapshot.Save(paths.SnapshotsDir(), time.Now(), img)
		}
	}
	return loop.Run()
}
