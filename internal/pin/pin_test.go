package pin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValid(t *testing.T) {
	for s, want := range map[string]bool{
		"1234": true, "0000": true, "": false, "123": false, "12345": false,
		"12a4": false, "١٢٣٤": false, " 123": false, "12 4": false,
	} {
		if Valid([]byte(s)) != want {
			t.Errorf("Valid(%q) = %v", s, !want)
		}
	}
}

func TestHashVerify(t *testing.T) {
	h, err := Hash([]byte("4821"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$y$") || strings.Contains(h, "4821") {
		t.Fatalf("hash %q", h)
	}
	if !Verify([]byte("4821"), h) {
		t.Error("right PIN refused")
	}
	for _, bad := range []string{"4822", "0000", "", "48210", "4821 "} {
		if Verify([]byte(bad), h) {
			t.Errorf("accepted %q", bad)
		}
	}
	h2, _ := Hash([]byte("4821"))
	if h == h2 {
		t.Error("two hashes of one PIN are equal, so the salt isn't random")
	}
	if Verify([]byte("4821"), "") || Verify([]byte("4821"), "$5$salt$nothing") || Verify([]byte("4821"), "*0") {
		t.Error("accepted a hash that isn't yescrypt")
	}
	if _, err := Hash([]byte("abcd")); err == nil {
		t.Error("hashed something that isn't a PIN")
	}
}

func TestSaveLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pin")
	h, _ := Hash([]byte("1357"))
	if Exists(dir, "alice") {
		t.Fatal("exists before saving")
	}
	if err := Save(dir, "alice", h); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", st.Mode().Perm())
	}
	p, _ := Path(dir, "alice")
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", st.Mode().Perm())
	}
	got, err := Load(dir, "alice", os.Geteuid())
	if err != nil || got != h {
		t.Fatalf("Load = %q, %v", got, err)
	}
	// Replacing it leaves no temporary file behind
	h2, _ := Hash([]byte("2468"))
	if err := Save(dir, "alice", h2); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d files in the PIN dir", len(entries))
	}
	if got, _ := Load(dir, "alice", os.Geteuid()); got != h2 {
		t.Error("the new PIN wasn't saved")
	}

	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "alice", os.Geteuid()); err == nil {
		t.Error("loaded a PIN file the group can read")
	}
	os.Chmod(p, 0o600)
	if _, err := Load(dir, "alice", os.Geteuid()+1); err == nil {
		t.Error("loaded a PIN file owned by someone else")
	}
	if err := Remove(dir, "alice"); err != nil || Exists(dir, "alice") {
		t.Errorf("Remove: %v", err)
	}
	if _, err := Path(dir, "../etc"); err == nil {
		t.Error("path for a bad username")
	}
}

func TestFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pin")
	h, _ := Hash([]byte("1357"))
	if err := Save(dir, "alice", h); err != nil {
		t.Fatal(err)
	}
	if n, err := Fails(dir, "alice"); n != 0 || err != nil {
		t.Fatalf("fresh PIN has %d, %v", n, err)
	}

	// What the PAM module leaves behind
	counter := filepath.Join(dir, "alice.fail")
	os.WriteFile(counter, []byte("5\n"), 0o600)
	if n, err := Fails(dir, "alice"); n != 5 || err != nil {
		t.Errorf("Fails = %d, %v", n, err)
	}
	os.WriteFile(counter, []byte("lots"), 0o600)
	if _, err := Fails(dir, "alice"); err == nil {
		t.Error("a count that isn't a number was read as zero")
	}
	os.WriteFile(counter, []byte("-1"), 0o600)
	if _, err := Fails(dir, "alice"); err == nil {
		t.Error("a negative count was accepted")
	}

	// Setting a new PIN, or removing it, forgets the count
	os.WriteFile(counter, []byte("6\n"), 0o600)
	if err := Save(dir, "alice", h); err != nil {
		t.Fatal(err)
	}
	if n, _ := Fails(dir, "alice"); n != 0 {
		t.Errorf("a new PIN kept %d wrong tries", n)
	}
	os.WriteFile(counter, []byte("6\n"), 0o600)
	if err := Remove(dir, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(counter); err == nil {
		t.Error("clearing the PIN left its count behind")
	}
	if err := Remove(dir, "alice"); err == nil {
		t.Error("removing a missing PIN didn't say so")
	}
	if _, err := Fails(dir, "../etc"); err == nil {
		t.Error("count for a bad username")
	}
}

func TestSaveUser(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pin-user")
	h, _ := Hash([]byte("2468"))
	uid, gid := os.Geteuid(), os.Getegid()
	if err := SaveUser(dir, "alice", h, uid, gid); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o755 {
		t.Errorf("dir mode %v", st.Mode().Perm())
	}
	// Both files have to exist: the module that counts can't create one
	for _, f := range []string{"alice", "alice.fail"} {
		if st, err := os.Stat(filepath.Join(dir, f)); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, st, err)
		}
	}
	if got, err := Load(dir, "alice", uid); err != nil || got != h {
		t.Errorf("Load = %q, %v", got, err)
	}

	os.WriteFile(filepath.Join(dir, "alice.fail"), []byte("4\n"), 0o600)
	if err := ResetFailsUser(dir, "alice", uid, gid); err != nil {
		t.Fatal(err)
	}
	if c, _ := os.ReadFile(filepath.Join(dir, "alice.fail")); string(c) != "0\n" {
		t.Errorf("counter %q after a reset, the file has to stay", c)
	}
	if err := RemoveUser(dir, "alice"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%d files left", len(entries))
	}
}
