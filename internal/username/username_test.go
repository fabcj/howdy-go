package username

import (
	"strings"
	"testing"
)

func TestValid(t *testing.T) {
	for _, ok := range []string{"alice", "_x", "a-b_c1", "machine$", strings.Repeat("a", 32)} {
		if !Valid(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "../x", "a/b", "Root", "1a", "a b", "x.dat", strings.Repeat("a", 33), "a$b"} {
		if Valid(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
