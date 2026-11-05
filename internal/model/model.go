package model

import (
	"errors"
	"fmt"
	"math"

	"howdy-go/internal/pyfmt"
)

// EncodingSize is the length of a dlib face descriptor
const EncodingSize = 128

// Models is the whole file: a list of entries in file order
type Models struct {
	Entries []*Object
}

// Parse reads a model file, checking every entry has a usable "data" list
func Parse(data []byte) (*Models, error) {
	v, err := Decode(data)
	if err != nil {
		return nil, err
	}
	list, ok := v.([]Value)
	if !ok {
		return nil, errors.New("model file isn't a list")
	}
	m := &Models{}
	for i, e := range list {
		o, ok := e.(*Object)
		if !ok {
			return nil, fmt.Errorf("model %d isn't an object", i)
		}
		if _, err := encodings(o); err != nil {
			return nil, fmt.Errorf("model %d: %w", i, err)
		}
		m.Entries = append(m.Entries, o)
	}
	return m, nil
}

// Bytes encodes the file as Python's json.dump would
func (m *Models) Bytes() ([]byte, error) {
	list := make([]Value, len(m.Entries))
	for i, e := range m.Entries {
		list[i] = e
	}
	return Encode(list)
}

func number(v Value) (float64, bool) {
	switch n := v.(type) {
	case Int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func encodings(o *Object) ([][]float64, error) {
	raw, ok := o.Get("data")
	if !ok {
		return nil, errors.New(`no "data"`)
	}
	list, ok := raw.([]Value)
	if !ok {
		return nil, errors.New(`"data" isn't a list`)
	}
	out := make([][]float64, 0, len(list))
	for _, e := range list {
		vec, ok := e.([]Value)
		if !ok || len(vec) != EncodingSize {
			return nil, fmt.Errorf("encodings must be lists of %d numbers", EncodingSize)
		}
		enc := make([]float64, EncodingSize)
		for j, x := range vec {
			f, ok := number(x)
			if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, errors.New("encodings must be lists of numbers")
			}
			enc[j] = f
		}
		out = append(out, enc)
	}
	return out, nil
}

// Flat lists every stored encoding, and for each one the entry it came from.
// compare.py matches against the flattened list
func (m *Models) Flat() (encs [][]float64, owner []int) {
	for i, e := range m.Entries {
		list, _ := encodings(e)
		for _, enc := range list {
			encs = append(encs, enc)
			owner = append(owner, i)
		}
	}
	return encs, owner
}

// Label is an entry's label as Python's str() would show it
func Label(o *Object) string {
	return Str(o.Values["label"])
}

// Str formats a value the way Python's str() does for what models hold
func Str(v Value) string {
	switch v := v.(type) {
	case string:
		return v
	case Int:
		return fmt.Sprint(int64(v))
	case float64:
		return pyfmt.Float(v)
	case bool:
		if v {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	}
	return fmt.Sprint(v)
}

// NextID is what howdy add uses: the last entry's id plus one, or 0
func (m *Models) NextID() (int64, error) {
	if len(m.Entries) == 0 {
		return 0, nil
	}
	v, ok := m.Entries[len(m.Entries)-1].Get("id")
	if !ok {
		return 0, errors.New(`the last model has no "id"`)
	}
	id, ok := v.(Int)
	if !ok {
		return 0, errors.New(`the last model's "id" isn't a whole number`)
	}
	return int64(id) + 1, nil
}

// NewEntry builds an entry with the key order howdy add writes
func NewEntry(time int64, label string, id int64, enc []float64) *Object {
	vec := make([]Value, len(enc))
	for i, f := range enc {
		vec[i] = f
	}
	o := &Object{}
	o.Set("time", Int(time))
	o.Set("label", label)
	o.Set("id", Int(id))
	o.Set("data", []Value{vec})
	return o
}
