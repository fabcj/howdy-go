// v4l2bench prints how long a camera takes from open to its first frame.
// Frames are never kept or written anywhere.
//
//	v4l2bench [-n runs] [-w width] [-h height] /dev/videoN
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"howdy-go/internal/v4l2"
)

func main() {
	runs := flag.Int("n", 1, "number of runs")
	width := flag.Int("w", -1, "frame width")
	height := flag.Int("h", -1, "frame height")
	gap := flag.Duration("gap", 0, "wait between runs, so the camera can autosuspend")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: v4l2bench [-n runs] [-w width] [-h height] [-gap 5s] /dev/videoN")
		os.Exit(2)
	}

	for i := 0; i < *runs; i++ {
		if i > 0 {
			time.Sleep(*gap)
		}
		start := time.Now()
		dev, err := v4l2.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		cam, err := v4l2.Start(dev, *width, *height, -1)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		opened := time.Since(start)
		_, err = cam.Read(2*time.Second, nil)
		first := time.Since(start)
		cam.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%dx%d %s  streaming after %.1f ms, first frame after %.1f ms\n",
			cam.Width, cam.Height, fourcc(cam.Pixelformat), ms(opened), ms(first))
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func fourcc(v uint32) string {
	return string([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
}
