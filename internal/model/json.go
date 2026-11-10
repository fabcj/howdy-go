// Package model reads and writes face model files, /etc/howdy/models/<user>.dat.
//
// They're JSON written by Python's json.dump with default options, and files
// get loaded and dumped again on every change. To give the same bytes Python
// would, values are kept in file order and ints stay apart from floats.
package model

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/fabcj/howdy-go/internal/pyfmt"
)

// Value is one of nil, bool, Int, float64, string, []Value or *Object
type Value any

// Int is a JSON number without a fraction or exponent
type Int int64

// Object keeps its keys in the order they were read or added
type Object struct {
	Keys   []string
	Values map[string]Value
}

// Get returns a key's value
func (o *Object) Get(key string) (Value, bool) {
	v, ok := o.Values[key]
	return v, ok
}

// Set adds or replaces a key, new keys go last
func (o *Object) Set(key string, v Value) {
	if o.Values == nil {
		o.Values = map[string]Value{}
	}
	if _, ok := o.Values[key]; !ok {
		o.Keys = append(o.Keys, key)
	}
	o.Values[key] = v
}

const maxDepth = 32

// Decode parses JSON the way Python's json.loads does, minus what it accepts
// that has no business in a model file: NaN and Infinity, duplicate keys,
// lone surrogates and integers that don't fit in 64 bits
func Decode(data []byte) (Value, error) {
	d := decoder{s: string(data)}
	d.space()
	v, err := d.value(0)
	if err != nil {
		return nil, err
	}
	d.space()
	if d.i != len(d.s) {
		return nil, d.errorf("extra data")
	}
	return v, nil
}

type decoder struct {
	s string
	i int
}

func (d *decoder) errorf(format string, args ...any) error {
	return fmt.Errorf("model JSON at byte %d: %s", d.i, fmt.Sprintf(format, args...))
}

func (d *decoder) space() {
	for d.i < len(d.s) {
		switch d.s[d.i] {
		case ' ', '\t', '\n', '\r':
			d.i++
		default:
			return
		}
	}
}

func (d *decoder) value(depth int) (Value, error) {
	if depth > maxDepth {
		return nil, d.errorf("nested too deep")
	}
	if d.i >= len(d.s) {
		return nil, d.errorf("unexpected end")
	}
	switch c := d.s[d.i]; {
	case c == '{':
		return d.object(depth)
	case c == '[':
		return d.array(depth)
	case c == '"':
		return d.str()
	case c == '-' || (c >= '0' && c <= '9'):
		return d.number()
	case strings.HasPrefix(d.s[d.i:], "true"):
		d.i += 4
		return true, nil
	case strings.HasPrefix(d.s[d.i:], "false"):
		d.i += 5
		return false, nil
	case strings.HasPrefix(d.s[d.i:], "null"):
		d.i += 4
		return nil, nil
	}
	return nil, d.errorf("unexpected character %q", d.s[d.i])
}

func (d *decoder) object(depth int) (Value, error) {
	o := &Object{Values: map[string]Value{}}
	d.i++
	d.space()
	if d.i < len(d.s) && d.s[d.i] == '}' {
		d.i++
		return o, nil
	}
	for {
		d.space()
		if d.i >= len(d.s) || d.s[d.i] != '"' {
			return nil, d.errorf("expected a key")
		}
		k, err := d.str()
		if err != nil {
			return nil, err
		}
		if _, dup := o.Values[k]; dup {
			return nil, d.errorf("key %q appears twice", k)
		}
		d.space()
		if d.i >= len(d.s) || d.s[d.i] != ':' {
			return nil, d.errorf("expected ':'")
		}
		d.i++
		d.space()
		v, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}
		o.Keys = append(o.Keys, k)
		o.Values[k] = v
		d.space()
		if d.i >= len(d.s) {
			return nil, d.errorf("unexpected end")
		}
		if d.s[d.i] == '}' {
			d.i++
			return o, nil
		}
		if d.s[d.i] != ',' {
			return nil, d.errorf("expected ',' or '}'")
		}
		d.i++
	}
}

func (d *decoder) array(depth int) (Value, error) {
	a := []Value{}
	d.i++
	d.space()
	if d.i < len(d.s) && d.s[d.i] == ']' {
		d.i++
		return a, nil
	}
	for {
		d.space()
		v, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}
		a = append(a, v)
		d.space()
		if d.i >= len(d.s) {
			return nil, d.errorf("unexpected end")
		}
		if d.s[d.i] == ']' {
			d.i++
			return a, nil
		}
		if d.s[d.i] != ',' {
			return nil, d.errorf("expected ',' or ']'")
		}
		d.i++
	}
}

func (d *decoder) number() (Value, error) {
	start := d.i
	if d.s[d.i] == '-' {
		d.i++
	}
	digits := func() int {
		n := 0
		for d.i < len(d.s) && d.s[d.i] >= '0' && d.s[d.i] <= '9' {
			d.i++
			n++
		}
		return n
	}
	intStart := d.i
	if digits() == 0 {
		return nil, d.errorf("bad number")
	}
	if d.s[intStart] == '0' && d.i-intStart > 1 {
		return nil, d.errorf("leading zero")
	}
	isFloat := false
	if d.i < len(d.s) && d.s[d.i] == '.' {
		d.i++
		if digits() == 0 {
			return nil, d.errorf("bad number")
		}
		isFloat = true
	}
	if d.i < len(d.s) && (d.s[d.i] == 'e' || d.s[d.i] == 'E') {
		d.i++
		if d.i < len(d.s) && (d.s[d.i] == '+' || d.s[d.i] == '-') {
			d.i++
		}
		if digits() == 0 {
			return nil, d.errorf("bad number")
		}
		isFloat = true
	}
	text := d.s[start:d.i]
	if !isFloat {
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, d.errorf("integer out of range")
		}
		return Int(n), nil
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(f, 0) {
		return nil, d.errorf("number out of range")
	}
	return f, nil
}

func (d *decoder) hex4() (rune, error) {
	if d.i+4 > len(d.s) {
		return 0, d.errorf("short \\u escape")
	}
	n, err := strconv.ParseUint(d.s[d.i:d.i+4], 16, 32)
	if err != nil {
		return 0, d.errorf("bad \\u escape")
	}
	d.i += 4
	return rune(n), nil
}

func (d *decoder) str() (string, error) {
	d.i++
	var b strings.Builder
	for {
		if d.i >= len(d.s) {
			return "", d.errorf("unterminated string")
		}
		c := d.s[d.i]
		switch {
		case c == '"':
			d.i++
			return b.String(), nil
		case c < 0x20:
			return "", d.errorf("control character in string")
		case c == '\\':
			d.i++
			if d.i >= len(d.s) {
				return "", d.errorf("unterminated string")
			}
			e := d.s[d.i]
			d.i++
			switch e {
			case '"', '\\', '/':
				b.WriteByte(e)
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				r, err := d.hex4()
				if err != nil {
					return "", err
				}
				if utf16.IsSurrogate(r) {
					if r >= 0xdc00 || !strings.HasPrefix(d.s[d.i:], "\\u") {
						return "", d.errorf("lone surrogate")
					}
					d.i += 2
					r2, err := d.hex4()
					if err != nil {
						return "", err
					}
					r = utf16.DecodeRune(r, r2)
					if r == utf8.RuneError {
						return "", d.errorf("lone surrogate")
					}
				}
				b.WriteRune(r)
			default:
				return "", d.errorf("bad escape")
			}
		default:
			r, size := utf8.DecodeRuneInString(d.s[d.i:])
			if r == utf8.RuneError && size == 1 {
				return "", d.errorf("invalid UTF-8")
			}
			b.WriteString(d.s[d.i : d.i+size])
			d.i += size
		}
	}
}

// Encode writes v like json.dumps(v) with default options
func Encode(v Value) ([]byte, error) {
	var b strings.Builder
	if err := encode(&b, v, 0); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func encode(b *strings.Builder, v Value, depth int) error {
	if depth > maxDepth {
		return errors.New("nested too deep")
	}
	switch v := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case Int:
		b.WriteString(strconv.FormatInt(int64(v), 10))
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New("can't store NaN or infinity")
		}
		b.WriteString(pyfmt.Float(v))
	case string:
		encodeString(b, v)
	case []Value:
		b.WriteByte('[')
		for i, e := range v {
			if i > 0 {
				b.WriteString(", ")
			}
			if err := encode(b, e, depth+1); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case *Object:
		b.WriteByte('{')
		for i, k := range v.Keys {
			if i > 0 {
				b.WriteString(", ")
			}
			encodeString(b, k)
			b.WriteString(": ")
			if err := encode(b, v.Values[k], depth+1); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("can't encode %T", v)
	}
	return nil
}

// Same escapes as Python's ensure_ascii, everything outside space to ~
func encodeString(b *strings.Builder, s string) {
	const hex = "0123456789abcdef"
	u := func(r rune) {
		b.WriteString(`\u`)
		for shift := 12; shift >= 0; shift -= 4 {
			b.WriteByte(hex[(r>>shift)&0xf])
		}
	}
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r >= ' ' && r <= '~':
			b.WriteRune(r)
		case r > 0xffff:
			r1, r2 := utf16.EncodeRune(r)
			u(r1)
			u(r2)
		default:
			u(r)
		}
	}
	b.WriteByte('"')
}
