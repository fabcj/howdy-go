// save-frontal-detector writes dlib's built in HOG face detector to a file,
// which loads much faster than building it at start up. Only the PKGBUILD
// runs it.
package main

import (
	"fmt"
	"os"

	"github.com/fabcj/howdy-go/internal/dlib"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: save-frontal-detector <file>")
		os.Exit(2)
	}
	if err := dlib.SaveFrontalDetector(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "save-frontal-detector:", err)
		os.Exit(1)
	}
}
