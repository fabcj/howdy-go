package model

import (
	"errors"
	"fmt"
	"math"

	"github.com/fabcj/howdy-go/internal/pyfmt"
)

// EncodingSize is the length of a dlib face descriptor
const EncodingSize = 128

// Models is the whole file: a list of entries in file order
type Models struct {
	Entries []*Object
	size    int // encoding length, EncodingSize when 0
}

func (m *Models) encodingSize() int {
	if m.size == 0 {
		return EncodingSize
	}
	return m.size
}

// Parse reads a model file, checking every entry has a usable "data" list
func Parse(data []byte) (*Models, error) {
	return ParseSize(data, EncodingSize)
}

// ParseSize is Parse for encodings of another length than dlib's
func ParseSize(data []byte, size int) (*Models, error) {
	v, err := Decode(data)
	if err != nil {
		return nil, err
	}
	list, ok := v.([]Value)
	if !ok {
		return nil, errors.New("model file isn't a list")
	}
	m := &Models{size: size}
	for i, e := range list {
		o, ok := e.(*Object)
		if !ok {
			return nil, fmt.Errorf("model %d isn't an object", i)
		}
		if _, err := encodings(o, size); err != nil {
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

func encodings(o *Object, size int) ([][]float64, error) {
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
		if !ok || len(vec) != size {
			return nil, fmt.Errorf("encodings must be lists of %d numbers", size)
		}
		enc := make([]float64, size)
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
		list, _ := encodings(e, m.encodingSize())
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

// ID is an entry's "id", when it's a whole number
func ID(o *Object) (int64, bool) {
	v, ok := o.Get("id")
	if !ok {
		return 0, false
	}
	id, ok := v.(Int)
	return int64(id), ok
}

// key ties an entry in another backend's file to its .dat entry: the id
// alone gets reused (NextID after removing the newest model), the time
// howdy add stamped on both doesn't
type key struct{ id, time int64 }

func keyOf(o *Object) (key, bool) {
	id, ok := ID(o)
	if !ok {
		return key{}, false
	}
	v, ok := o.Get("time")
	if !ok {
		return key{}, false
	}
	t, ok := v.(Int)
	return key{id, int64(t)}, ok
}

func (m *Models) index() map[key]int {
	index := map[key]int{}
	for i, e := range m.Entries {
		if k, ok := keyOf(e); ok {
			if _, dup := index[k]; !dup {
				index[k] = i
			}
		}
	}
	return index
}

// Enrolled lists m's encodings from the given encoder whose id and time
// are also in enrolled, the user's .dat file, which list, remove and clear
// go by. So an encoding whose model was removed from the .dat file, by
// howdy-gtk or Python Howdy too, can never match, even once the id is
// reused. owner is the index of the entry in enrolled each one belongs to.
func (m *Models) Enrolled(enrolled *Models, encoder string) (encs [][]float64, owner []int) {
	index := enrolled.index()
	for _, e := range m.Entries {
		if tag, _ := e.Get("encoder"); tag != encoder {
			continue
		}
		k, ok := keyOf(e)
		if !ok {
			continue
		}
		i, ok := index[k]
		if !ok {
			continue
		}
		list, _ := encodings(e, m.encodingSize())
		for _, enc := range list {
			encs = append(encs, enc)
			owner = append(owner, i)
		}
	}
	return encs, owner
}

// Prune removes the entries Enrolled would never use, whose model isn't
// in enrolled any more
func (m *Models) Prune(enrolled *Models) {
	index := enrolled.index()
	kept := m.Entries[:0]
	for _, e := range m.Entries {
		if k, ok := keyOf(e); ok {
			if _, ok := index[k]; ok {
				kept = append(kept, e)
			}
		}
	}
	m.Entries = kept
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

// NewTaggedEntry is NewEntry with the encoder that made enc, for backends
// other than dlib
func NewTaggedEntry(time int64, label string, id int64, enc []float64, encoder string) *Object {
	o := NewEntry(time, label, id, enc)
	o.Set("encoder", encoder)
	return o
}

// NewModels is an empty file of encodings size long
func NewModels(size int) *Models {
	return &Models{size: size}
}
