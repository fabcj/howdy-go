//go:build !noonnx

// Package onnx runs ONNX models with ONNX Runtime. A model file is read
// and checked against a pinned SHA-256 before ONNX Runtime sees any of it,
// and the bytes that were checked are the ones it loads.
package onnx

/*
#cgo pkg-config: libonnxruntime
#cgo CFLAGS: -std=c11 -D_GNU_SOURCE
#include <stdlib.h>
#include "ort.h"
*/
import "C"

import (
	"errors"
	"runtime"
	"sync"
	"unsafe"
)

// Available is true when this build has ONNX Runtime
const Available = true

// Session is one loaded model. Run is safe to call from one goroutine at a
// time; the mutex keeps it that way.
type Session struct {
	mu sync.Mutex
	s  *C.hd_ort
}

// Open reads the model at path, checks it hashes to sha256Hex, and loads it
func Open(path, sha256Hex string) (*Session, error) {
	data, err := readPinned(path, sha256Hex)
	if err != nil {
		return nil, err
	}
	var buf [C.HD_ORT_ERRLEN]C.char
	s := C.hd_ort_open(unsafe.Pointer(&data[0]), C.size_t(len(data)), &buf[0])
	runtime.KeepAlive(data)
	if s == nil {
		return nil, errors.New(path + ": " + C.GoString(&buf[0]))
	}
	return &Session{s: s}, nil
}

// Output0 is the name of the model's first output
func (s *Session) Output0() string {
	return C.GoString(C.hd_ort_output0(s.s))
}

// Outputs is the set of outputs Run fetches, with the most values each may
// have. Build one per model and keep it: the names are C strings, made
// once, so Run passes no Go pointer inside a C array.
type Outputs struct {
	n     int
	names unsafe.Pointer // char ** in C memory
	caps  []C.size_t
	Total int // floats needed for every output at its cap
}

// Outputs prepares names and caps for Run
func (s *Session) Outputs(names []string, caps []int) (*Outputs, error) {
	if len(names) != len(caps) || len(names) == 0 || len(names) > 16 {
		return nil, errors.New("onnx: bad outputs")
	}
	o := &Outputs{n: len(names), caps: make([]C.size_t, len(names))}
	o.names = C.malloc(C.size_t(len(names)) * C.size_t(unsafe.Sizeof(uintptr(0))))
	arr := unsafe.Slice((**C.char)(o.names), len(names))
	for i, name := range names {
		arr[i] = C.CString(name)
		o.caps[i] = C.size_t(caps[i])
		o.Total += caps[i]
	}
	return o, nil
}

// Close frees the names
func (o *Outputs) Close() {
	if o.names == nil {
		return
	}
	for _, p := range unsafe.Slice((**C.char)(o.names), o.n) {
		C.free(unsafe.Pointer(p))
	}
	C.free(o.names)
	o.names = nil
}

// Run feeds input, shaped dims, to the model and returns one slice per
// output, in o's order. They alias dst, which must hold o.Total floats, and
// are only good until the next Run with the same dst.
func (s *Session) Run(input []float32, dims []int64, o *Outputs, dst []float32) ([][]float32, error) {
	n := 1
	for _, d := range dims {
		n *= int(d)
	}
	if len(dims) == 0 || len(input) == 0 || n != len(input) || len(dst) < o.Total {
		return nil, errors.New("onnx: bad input shape or output buffer")
	}
	cdims := make([]C.int64_t, len(dims))
	for i, d := range dims {
		cdims[i] = C.int64_t(d)
	}
	lens := make([]C.size_t, o.n)
	var buf [C.HD_ORT_ERRLEN]C.char

	s.mu.Lock()
	if s.s == nil {
		s.mu.Unlock()
		return nil, errors.New("onnx: session is closed")
	}
	rc := C.hd_ort_run(s.s, &cdims[0], C.int(len(dims)), (*C.float)(unsafe.Pointer(&input[0])),
		(**C.char)(o.names), C.int(o.n), (*C.float)(unsafe.Pointer(&dst[0])), &o.caps[0], &lens[0], &buf[0])
	s.mu.Unlock()
	if rc < 0 {
		return nil, errors.New("onnx: " + C.GoString(&buf[0]))
	}
	out := make([][]float32, o.n)
	off := 0
	for i := range out {
		out[i] = dst[off : off+int(lens[i]) : off+int(o.caps[i])]
		off += int(o.caps[i])
	}
	return out, nil
}

// Close frees the session, more than once is fine
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.s != nil {
		C.hd_ort_close(s.s)
		s.s = nil
	}
}
