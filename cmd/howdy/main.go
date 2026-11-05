// howdy manages face models and the config, like Howdy's Python CLI
package main

import (
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"syscall"
	"time"

	"howdy-go/internal/camera"
	"howdy-go/internal/cli"
	"howdy-go/internal/compare"
	"howdy-go/internal/config"
	"howdy-go/internal/dlib"
	"howdy-go/internal/face"
	"howdy-go/internal/paths"
)

func main() {
	os.Exit(cli.Main(&cli.Env{
		Args:   os.Args[1:],
		Getenv: os.Getenv,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Euid:   os.Geteuid(),
		UserName: func(uid string) string {
			u, err := user.LookupId(uid)
			if err != nil {
				return ""
			}
			return u.Username
		},
		CurrentUser: func() string {
			u, err := user.Current()
			if err != nil {
				return ""
			}
			return u.Username
		},
		UserGID: cli.UserGID,
		Camera: func(s *config.Settings) (compare.Camera, func(), error) {
			c, err := camera.Open(s)
			if err != nil {
				return nil, nil, err
			}
			return c, c.Close, nil
		},
		Faces: func(s *config.Settings) (face.Recognizer, error) {
			m, err := dlib.Load(paths.HOGDetector(), paths.CNNDetector(), paths.ShapePredictor(), paths.Recognizer(), s.UseCNN)
			if err != nil {
				return nil, err
			}
			return m, nil
		},
		Edit: func(argv []string) error {
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			return cmd.Run()
		},
		LookPath: exec.LookPath,
		Now:      time.Now,
		Sleep:    time.Sleep,
		Interrupts: func() (<-chan os.Signal, func()) {
			ch := make(chan os.Signal, 1)
			signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
			return ch, func() { signal.Stop(ch) }
		},
	}))
}
