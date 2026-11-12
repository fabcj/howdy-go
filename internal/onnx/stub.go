//go:build noonnx

// Package onnx is built without ONNX Runtime here, for working on
// everything else. Release builds never use the noonnx tag.
package onnx

import "errors"

// Available is false in this build
const Available = false

var errNoONNX = errors.New("built without ONNX Runtime (noonnx tag)")

type Session struct{}

type Outputs struct{ Total int }

func Open(path, sha256Hex string) (*Session, error) {
	if _, err := readPinned(path, sha256Hex); err != nil {
		return nil, err
	}
	return nil, errNoONNX
}

func (s *Session) Output0() string { return "" }

func (s *Session) Outputs([]string, []int) (*Outputs, error) { return nil, errNoONNX }

func (o *Outputs) Close() {}

func (s *Session) Run([]float32, []int64, *Outputs, []float32) ([][]float32, error) {
	return nil, errNoONNX
}

func (s *Session) Close() {}
