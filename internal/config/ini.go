// Package config reads Howdy's config.ini.
//
// Two different parsers read that file: Python's configparser (the old
// compare and CLI) and inih's INIReader (the PAM module). They disagree on
// some input, so this parser only accepts files where both would read the
// same thing, and refuses the rest instead of guessing: duplicate keys and
// sections, sections differing only in case, values with " ;", nan and
// inf, % interpolation and booleans neither parser agrees on.
package config

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// inih's INI_MAX_LINE is 200, which leaves 198 characters for the line
const maxLine = 198

// ErrMissing is returned by the getters when a key isn't set
var ErrMissing = errors.New("not set")

// File is a parsed config, which also remembers the raw lines so a single
// value can be rewritten without touching the rest
type File struct {
	sections map[string]*section
	order    []string
	lines    []string
}

type section struct {
	name   string
	line   int
	values map[string]*value
	keys   []string
}

type value struct {
	raw  string
	line int // index into lines of the key line
	last int // index of the last continuation line

	indented bool
}

// ParseError points at the line that made the file unreadable
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("config.ini line %d: %s", e.Line, e.Msg)
}

var sectionRe = regexp.MustCompile(`^\[([^\[\]]+)\]$`)

// Parse reads config.ini
func Parse(data []byte) (*File, error) {
	text := string(data)
	if strings.HasPrefix(text, "\xef\xbb\xbf") {
		return nil, &ParseError{1, "starts with a byte order mark"}
	}

	f := &File{sections: map[string]*section{}}
	f.lines = strings.Split(text, "\n")
	var cur *section
	var prev *value

	for i, line := range f.lines {
		n := i + 1
		line = strings.TrimSuffix(line, "\r")
		if len(line) > maxLine {
			return nil, &ParseError{n, "line is longer than the PAM module can read"}
		}

		stripped := strings.TrimSpace(line)
		if stripped == "" {
			continue
		}
		if stripped[0] == '#' || stripped[0] == ';' {
			continue
		}

		// Indented lines continue the previous value in both parsers, as long
		// as the key itself wasn't indented. Python compares indent levels
		// there and inih doesn't
		indented := line[0] == ' ' || line[0] == '\t'
		if prev != nil && indented {
			if prev.indented {
				return nil, &ParseError{n, "indented line after an indented key"}
			}
			if prev.raw == "" {
				prev.raw = stripped
			} else {
				prev.raw += "\n" + stripped
			}
			prev.last = i
			continue
		}

		if stripped[0] == '[' {
			m := sectionRe.FindStringSubmatch(stripped)
			if m == nil {
				return nil, &ParseError{n, "malformed section header"}
			}
			name := m[1]
			if strings.EqualFold(name, "default") {
				return nil, &ParseError{n, "a [DEFAULT] section changes every other section in Python"}
			}
			for _, other := range f.order {
				if strings.EqualFold(other, name) {
					return nil, &ParseError{n, fmt.Sprintf("section [%s] appears twice", name)}
				}
			}
			cur = &section{name: name, line: i, values: map[string]*value{}}
			f.sections[name] = cur
			f.order = append(f.order, name)
			prev = nil
			continue
		}

		if cur == nil {
			return nil, &ParseError{n, "setting outside of any section"}
		}

		at := strings.IndexAny(stripped, "=:")
		if at < 0 {
			return nil, &ParseError{n, "expected key = value"}
		}
		key := strings.ToLower(strings.TrimSpace(stripped[:at]))
		if key == "" {
			return nil, &ParseError{n, "empty key"}
		}
		if _, dup := cur.values[key]; dup {
			return nil, &ParseError{n, fmt.Sprintf("%q is set twice in [%s]", key, cur.name)}
		}
		v := &value{raw: strings.TrimSpace(stripped[at+1:]), line: i, last: i, indented: indented}
		cur.values[key] = v
		cur.keys = append(cur.keys, key)
		prev = v
	}

	return f, nil
}

func (f *File) lookup(sec, key string) (*value, bool) {
	s, ok := f.sections[sec]
	if !ok {
		return nil, false
	}
	v, ok := s.values[strings.ToLower(key)]
	return v, ok
}

// inlineSemicolon is where inih would cut a value off as a comment
var inlineSemicolon = regexp.MustCompile(`\s;`)

// Raw returns a value as Python would see it, refusing values that the PAM
// module or Python's interpolation would read differently
func (f *File) Raw(sec, key string) (string, error) {
	v, ok := f.lookup(sec, key)
	if !ok {
		return "", ErrMissing
	}
	if v.last != v.line {
		return "", fmt.Errorf("[%s] %s: expected a single line", sec, key)
	}
	if inlineSemicolon.MatchString(v.raw) {
		return "", fmt.Errorf("[%s] %s: the PAM module reads \" ;\" as a comment, Python doesn't", sec, key)
	}
	if strings.ContainsRune(v.raw, '%') {
		return "", fmt.Errorf("[%s] %s: %% isn't allowed in values", sec, key)
	}
	return v.raw, nil
}

// String returns a value or the fallback if it isn't set
func (f *File) String(sec, key, fallback string) (string, error) {
	s, err := f.Raw(sec, key)
	if errors.Is(err, ErrMissing) {
		return fallback, nil
	}
	return s, err
}

// Bool accepts the words both parsers accept, in any case
func (f *File) Bool(sec, key string, fallback bool) (bool, error) {
	s, err := f.Raw(sec, key)
	if errors.Is(err, ErrMissing) {
		return fallback, nil
	}
	if err != nil {
		return false, err
	}
	switch strings.ToLower(s) {
	case "1", "yes", "true", "on":
		return true, nil
	case "0", "no", "false", "off":
		return false, nil
	}
	return false, fmt.Errorf("[%s] %s: %q isn't a boolean", sec, key, s)
}

var intRe = regexp.MustCompile(`^[+-]?[0-9]+$`)

// Int accepts plain base 10 integers, as Python's int() does without the
// underscores and unicode digits it also takes
func (f *File) Int(sec, key string, fallback int) (int, error) {
	s, err := f.Raw(sec, key)
	if errors.Is(err, ErrMissing) {
		return fallback, nil
	}
	if err != nil {
		return 0, err
	}
	if !intRe.MatchString(s) {
		return 0, fmt.Errorf("[%s] %s: %q isn't a whole number", sec, key, s)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("[%s] %s: %q is out of range", sec, key, s)
	}
	return n, nil
}

var floatRe = regexp.MustCompile(`^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?$`)

// Float accepts decimal and exponent notation. Unlike Python it refuses inf
// and nan, a certainty of nan would silently never match
func (f *File) Float(sec, key string, fallback float64) (float64, error) {
	s, err := f.Raw(sec, key)
	if errors.Is(err, ErrMissing) {
		return fallback, nil
	}
	if err != nil {
		return 0, err
	}
	if !floatRe.MatchString(s) {
		return 0, fmt.Errorf("[%s] %s: %q isn't a number", sec, key, s)
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("[%s] %s: %q is out of range", sec, key, s)
	}
	return n, nil
}

// Has reports whether a key is set
func (f *File) Has(sec, key string) bool {
	_, ok := f.lookup(sec, key)
	return ok
}

// Sections lists the section names in file order
func (f *File) Sections() []string {
	return append([]string(nil), f.order...)
}

// SectionsWithKey lists the sections that set key
func (f *File) SectionsWithKey(key string) []string {
	var out []string
	for _, name := range f.order {
		if _, ok := f.sections[name].values[strings.ToLower(key)]; ok {
			out = append(out, name)
		}
	}
	return out
}

// Set rewrites a single value and returns the new file contents. Everything
// else stays byte for byte. A missing key is added at the end of its
// section, and the section must exist.
func (f *File) Set(sec, key, val string) ([]byte, error) {
	if strings.ContainsAny(val, "\r\n") {
		return nil, errors.New("values can't contain line breaks")
	}
	key = strings.ToLower(key)
	lines := append([]string(nil), f.lines...)
	newLine := key + " = " + val

	if v, ok := f.lookup(sec, key); ok {
		cr := ""
		if strings.HasSuffix(lines[v.line], "\r") {
			cr = "\r"
		}
		out := append([]string(nil), lines[:v.line]...)
		out = append(out, newLine+cr)
		out = append(out, lines[v.last+1:]...)
		lines = out
	} else {
		s, ok := f.sections[sec]
		if !ok {
			return nil, fmt.Errorf("there's no [%s] section", sec)
		}
		at := s.line
		for _, k := range s.keys {
			if l := s.values[k].last; l > at {
				at = l
			}
		}
		out := append([]string(nil), lines[:at+1]...)
		out = append(out, newLine)
		out = append(out, lines[at+1:]...)
		lines = out
	}

	data := []byte(strings.Join(lines, "\n"))
	// Never write something this parser would refuse to read back
	check, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if _, err := check.Raw(sec, key); err != nil {
		return nil, err
	}
	return data, nil
}
