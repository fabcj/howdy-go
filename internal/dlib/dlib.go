//go:build !nodlib

// Package dlib wraps the four dlib models compare uses. Every call goes
// through one locked OS thread: dlib objects aren't safe to share.
package dlib

/*
#cgo pkg-config: dlib-1
#cgo CXXFLAGS: -std=c++17
#include <stdlib.h>
#include "face.h"

// Before anything else runs, so BLAS never starts its thread pool
__attribute__((constructor)) static void howdy_blas_init(void) {
	hd_blas_single_thread();
}
*/
import "C"

import (
	"errors"
	"runtime"
	"unsafe"

	"github.com/fabcj/howdy-go/internal/face"
	"github.com/fabcj/howdy-go/internal/imgproc"
)

// Available is true when this build has dlib
const Available = true

// Models holds the loaded dlib models
type Models struct {
	calls    chan func()
	f        *C.hd_face
	upsample int
	useCNN   bool
}

func errorFrom(buf []C.char) error {
	return errors.New(C.GoString(&buf[0]))
}

// Load loads the detector (the HOG .svm, or the MMOD CNN with useCNN), the
// 5 point shape predictor and the ResNet encoder. Detect upsamples images
// upsample times.
func Load(hogPath, cnnPath, shapePath, encoderPath string, useCNN bool, upsample int) (*Models, error) {
	m := &Models{calls: make(chan func()), upsample: upsample, useCNN: useCNN}
	go func() {
		runtime.LockOSThread()
		for fn := range m.calls {
			fn()
		}
	}()

	var err error
	m.do(func() {
		m.f = C.hd_new()
		if m.f == nil {
			err = errors.New("out of memory")
			return
		}
		load := func(fn func(*C.hd_face, *C.char, *C.char) C.int, path string) error {
			cpath := C.CString(path)
			defer C.free(unsafe.Pointer(cpath))
			buf := make([]C.char, C.HD_ERRLEN)
			if fn(m.f, cpath, &buf[0]) < 0 {
				return errors.New(path + ": " + C.GoString(&buf[0]))
			}
			return nil
		}
		if useCNN {
			err = load(func(f *C.hd_face, p, e *C.char) C.int { return C.hd_load_cnn(f, p, e) }, cnnPath)
		} else {
			err = load(func(f *C.hd_face, p, e *C.char) C.int { return C.hd_load_hog(f, p, e) }, hogPath)
			// Like compare.py, fall back to the built in copy if the .svm won't load
			if err != nil {
				buf := make([]C.char, C.HD_ERRLEN)
				if C.hd_use_builtin_hog(m.f, &buf[0]) == 0 {
					err = nil
				}
			}
		}
		if err == nil {
			err = load(func(f *C.hd_face, p, e *C.char) C.int { return C.hd_load_shape(f, p, e) }, shapePath)
		}
		if err == nil {
			err = load(func(f *C.hd_face, p, e *C.char) C.int { return C.hd_load_encoder(f, p, e) }, encoderPath)
		}
	})
	if err != nil {
		m.Close()
		return nil, err
	}
	return m, nil
}

func (m *Models) do(fn func()) {
	done := make(chan struct{})
	m.calls <- func() {
		fn()
		close(done)
	}
	<-done
}

// Close frees the models
func (m *Models) Close() {
	if m.calls == nil {
		return
	}
	m.do(func() {
		if m.f != nil {
			C.hd_free(m.f)
			m.f = nil
		}
	})
	close(m.calls)
	m.calls = nil
}

const maxFaces = 32

// Detect finds faces in a grey image, in dlib's order, the way Load was told to
func (m *Models) Detect(g *imgproc.Gray) ([]face.Rect, error) {
	return m.DetectWith(g, m.upsample, m.useCNN)
}

// DetectWith is Detect with another upsample, or the other detector (which
// has to be loaded)
func (m *Models) DetectWith(g *imgproc.Gray, upsample int, useCNN bool) ([]face.Rect, error) {
	if len(g.Pix) == 0 || len(g.Pix) != g.W*g.H {
		return nil, errors.New("empty image")
	}
	var out []face.Rect
	var err error
	m.do(func() {
		rects := make([]C.long, maxFaces*4)
		buf := make([]C.char, C.HD_ERRLEN)
		cnn := C.int(0)
		if useCNN {
			cnn = 1
		}
		n := C.hd_detect(m.f, (*C.uint8_t)(unsafe.Pointer(&g.Pix[0])), C.int(g.W), C.int(g.H),
			C.int(upsample), cnn, &rects[0], maxFaces, &buf[0])
		if n < 0 {
			err = errorFrom(buf)
			return
		}
		for i := 0; i < int(n) && i < maxFaces; i++ {
			out = append(out, face.Rect{
				Left: int(rects[i*4]), Top: int(rects[i*4+1]),
				Right: int(rects[i*4+2]), Bottom: int(rects[i*4+3]),
			})
		}
	})
	return out, err
}

// Encode finds the landmarks in a face box and returns its descriptor
func (m *Models) Encode(g *imgproc.Gray, r face.Rect) (face.Encoding, error) {
	enc := face.Encoding{Descriptor: make([]float64, 128)}
	if len(g.Pix) == 0 || len(g.Pix) != g.W*g.H {
		return enc, errors.New("empty image")
	}
	var err error
	m.do(func() {
		rect := []C.long{C.long(r.Left), C.long(r.Top), C.long(r.Right), C.long(r.Bottom)}
		parts := make([]C.long, 10)
		desc := make([]C.double, 128)
		buf := make([]C.char, C.HD_ERRLEN)
		if C.hd_encode(m.f, (*C.uint8_t)(unsafe.Pointer(&g.Pix[0])), C.int(g.W), C.int(g.H),
			&rect[0], &desc[0], &parts[0], &buf[0]) < 0 {
			err = errorFrom(buf)
			return
		}
		for i := range enc.Descriptor {
			enc.Descriptor[i] = float64(desc[i])
		}
		for i := range enc.Parts {
			enc.Parts[i] = [2]int{int(parts[i*2]), int(parts[i*2+1])}
		}
	})
	return enc, err
}

// SaveFrontalDetector writes dlib's built in HOG face detector, the
// frontal_face_detector.svm that Load reads
func SaveFrontalDetector(path string) error {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	buf := make([]C.char, C.HD_ERRLEN)
	if C.hd_save_frontal(cpath, &buf[0]) < 0 {
		return errorFrom(buf)
	}
	return nil
}
