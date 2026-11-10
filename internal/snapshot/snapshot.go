// Package snapshot saves a picture of a login attempt, like Howdy's
// snapshot.py: the frames side by side over a grey strip of text.
package snapshot

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"golang.org/x/sys/unix"

	"github.com/fabcj/howdy-go/internal/imgproc"
)

var (
	padColor  = color.RGBA{44, 44, 44, 255}
	textColor = color.RGBA{255, 255, 255, 255}
)

const (
	logoW = 180
	logoH = 57
)

// Build lays the frames out with the text lines underneath. The logo goes
// in the bottom right when there's more than one frame and it's the size
// snapshot.py expects. All frames must be the same size.
func Build(frames []*imgproc.Gray, lines []string, logo image.Image) (*image.RGBA, error) {
	if len(frames) == 0 {
		return nil, errors.New("no frames")
	}
	fw, fh := frames[0].W, frames[0].H
	for _, f := range frames {
		if f.W != fw || f.H != fh {
			return nil, errors.New("frames differ in size")
		}
	}

	w := fw * len(frames)
	h := fh + len(lines)*20 + 40
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{padColor}, image.Point{}, draw.Src)

	for i, f := range frames {
		for y := 0; y < fh; y++ {
			for x := 0; x < fw; x++ {
				v := f.Pix[y*fw+x]
				o := img.PixOffset(i*fw+x, y)
				img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = v, v, v, 255
			}
		}
	}

	if len(frames) > 1 && logo != nil && logo.Bounds().Dx() == logoW && logo.Bounds().Dy() == logoH {
		lx, ly := w-210, fh+20
		b := logo.Bounds()
		if lx >= 0 {
			for y := 0; y < logoH; y++ {
				for x := 0; x < logoW; x++ {
					// cv2.imread drops the alpha channel without blending
					c := color.NRGBAModel.Convert(logo.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
					img.SetRGBA(lx+x, ly+y, color.RGBA{c.R, c.G, c.B, 255})
				}
			}
		}
	}

	d := font.Drawer{Dst: img, Src: &image.Uniform{textColor}, Face: basicfont.Face7x13}
	for i, line := range lines {
		d.Dot = fixed.P(30, fh+30+i*20)
		d.DrawString(line)
	}
	return img, nil
}

// LoadLogo reads the logo PNG, nil if it can't be read
func LoadLogo(path string) image.Image {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return img
}

// Name is the file name for a snapshot taken at t
func Name(t time.Time) string {
	return t.UTC().Format("20060102T150405") + ".jpg"
}

// Save encodes img and writes it into dir, creating dir with mode 0750 if
// it's missing. The dir can be writable by the user while this runs as root
// and the names are predictable, so it never follows a link or replaces a
// file: a planted symlink would otherwise make root overwrite its target.
func Save(dir string, t time.Time, img image.Image) (string, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		return "", err
	}

	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return "", err
		}
	}
	dfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", &fs.PathError{Op: "open", Path: dir, Err: err}
	}
	defer unix.Close(dfd)

	name := Name(t)
	fd, err := unix.Openat(dfd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o640)
	if err != nil {
		return "", &fs.PathError{Op: "open", Path: filepath.Join(dir, name), Err: err}
	}
	f := os.NewFile(uintptr(fd), name)
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}
