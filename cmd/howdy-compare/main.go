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

	"howdy-go/internal/camera"
	"howdy-go/internal/compare"
	"howdy-go/internal/config"
	"howdy-go/internal/dlib"
	"howdy-go/internal/harden"
	"howdy-go/internal/imgproc"
	"howdy-go/internal/model"
	"howdy-go/internal/paths"
	"howdy-go/internal/snapshot"
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
	if !model.ValidUser(user) {
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
	known, owner := models.Flat()
	if len(known) == 0 {
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

	if _, err := os.Stat(s.DevicePath); err != nil {
		if s.WarnNoDevice {
			fmt.Println("Howdy could not find a camera device at the path specified in the config file.")
			fmt.Println("It is very likely that the path is not configured correctly, please edit the 'device_path' config value by running:")
			fmt.Print("\n\tsudo howdy config\n\n")
		}
		return compare.BadDevice
	}

	if _, err := os.Stat(paths.ShapePredictor()); err != nil {
		fmt.Println("Data files have not been downloaded, please run the following commands:")
		fmt.Println("\n\tcd " + paths.DlibDataDir)
		fmt.Print("\tsudo ./install.sh\n\n")
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
	faces, faceErr := dlib.Load(paths.HOGDetector(), paths.CNNDetector(), paths.ShapePredictor(), paths.Recognizer(), s.UseCNN)
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
		return fail(compare.Abort, "can't load the dlib models: %v", faceErr)
	}
	defer faces.Close()

	snapshots := s.SaveFailed || s.SaveSuccessful
	if unix.Geteuid() == 0 {
		// Started by sudo and the like: nothing past this point needs root.
		// Writing a snapshot would, so those are off here (spec 4.8)
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
