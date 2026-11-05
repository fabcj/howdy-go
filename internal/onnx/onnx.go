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
	buf := make([]C.char, C.HD_ORT_ERRLEN)
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

// Run feeds input, shaped dims, to the model and returns the named outputs.
// caps[i] is the most values output i may have.
func (s *Session) Run(input []float32, dims []int64, names []string, caps []int) ([][]float32, error) {
	n := 1
	for _, d := range dims {
		n *= int(d)
	}
	if len(dims) == 0 || n != len(input) || len(names) != len(caps) || len(names) == 0 || len(names) > 16 {
		return nil, errors.New("onnx: bad input shape or outputs")
	}

	// Everything C reads lives in C memory, so no Go pointer is passed in a
	// C array
	cnames := make([]*C.char, len(names))
	for i, name := range names {
		cnames[i] = C.CString(name)
	}
	defer func() {
		for _, p := range cnames {
			C.free(unsafe.Pointer(p))
		}
	}()
	cn := C.malloc(C.size_t(len(names)) * C.size_t(unsafe.Sizeof(uintptr(0))))
	co := C.malloc(C.size_t(len(names)) * C.size_t(unsafe.Sizeof(uintptr(0))))
	defer C.free(cn)
	defer C.free(co)
	nameArr := unsafe.Slice((**C.char)(cn), len(names))
	outArr := unsafe.Slice((**C.float)(co), len(names))
	ccaps := make([]C.size_t, len(names))
	lens := make([]C.size_t, len(names))
	for i := range names {
		nameArr[i] = cnames[i]
		outArr[i] = (*C.float)(C.malloc(C.size_t(max(caps[i], 1)) * 4))
		ccaps[i] = C.size_t(caps[i])
	}
	defer func() {
		for _, p := range outArr {
			C.free(unsafe.Pointer(p))
		}
	}()
	cdims := make([]C.int64_t, len(dims))
	for i, d := range dims {
		cdims[i] = C.int64_t(d)
	}

	buf := make([]C.char, C.HD_ORT_ERRLEN)
	s.mu.Lock()
	rc := C.hd_ort_run(s.s, &cdims[0], C.int(len(dims)), (*C.float)(unsafe.Pointer(&input[0])),
		(**C.char)(cn), C.int(len(names)), (**C.float)(co), &ccaps[0], &lens[0], &buf[0])
	s.mu.Unlock()
	if rc < 0 {
		return nil, errors.New("onnx: " + C.GoString(&buf[0]))
	}
	out := make([][]float32, len(names))
	for i := range names {
		out[i] = append([]float32(nil), unsafe.Slice((*float32)(unsafe.Pointer(outArr[i])), int(lens[i]))...)
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
