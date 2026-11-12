package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"

	"github.com/fabcj/howdy-go/internal/pin"
)

// pin manages the PIN the PAM module asks for after a face match, when
// pam_howdy.so has the "pin" argument, or "pin-user" for a lock screen, which
// runs as the user and keeps its PIN in a file the user owns
func (c *cmd) pin() int {
	sub, lock := "", false
	if len(c.a.arguments) > 0 {
		sub = c.a.arguments[0]
	}
	if len(c.a.arguments) > 1 {
		if c.a.arguments[1] != "lock-screen" || len(c.a.arguments) > 2 {
			sub = ""
		}
		lock = true
	}
	switch sub {
	case "set":
		return c.pinSet(lock)
	case "clear":
		return c.pinClear(lock)
	case "status":
		if lock {
			break
		}
		c.pinStatus("PIN", "sudo and login managers", c.Paths.PinDir)
		c.pinStatus("lock screen PIN", "lock screens", c.Paths.PinUserDir)
		return 0
	}
	c.say("Please say what to do with the PIN:")
	c.say("\n\thowdy pin set                 a 4 digit PIN for sudo and login managers")
	c.say("\thowdy pin set lock-screen     one for lock screens, which run as you")
	c.say("\thowdy pin clear [lock-screen] remove one")
	c.say("\thowdy pin status              show both\n")
	return 1
}

func (c *cmd) pinStatus(name, where, dir string) {
	if !pin.Exists(dir, c.user) {
		c.printf("%s: %s has none (for %s)\n", name, c.user, where)
		return
	}
	c.printf("%s: %s has one (for %s)\n", name, c.user, where)
	if n, err := pin.Fails(dir, c.user); err != nil {
		c.printf("  its wrong tries can't be read (%v), so it is treated as switched off\n", err)
	} else if n >= pin.MaxFails {
		c.printf("  switched off after %d wrong tries in a row, set it again to turn it on\n", n)
	} else if n > 0 {
		c.printf("  %d of %d wrong tries in a row so far, a right PIN resets them\n", n, pin.MaxFails)
	}
}

func (c *cmd) pinSet(lock bool) int {
	if c.ReadHidden == nil {
		return c.errorf("can't ask for a PIN here")
	}
	first, err := c.ReadHidden("New PIN (4 digits): ")
	if err != nil {
		return c.errorf("%v", err)
	}
	defer clear(first)
	if !pin.Valid(first) {
		return c.fail("A PIN is exactly 4 digits, nothing was changed")
	}
	if weak(first) {
		return c.fail("That PIN is among the first ones anyone tries (repeated digits, runs like 1234 and pairs like 1212), choose another")
	}
	second, err := c.ReadHidden("Repeat PIN: ")
	if err != nil {
		return c.errorf("%v", err)
	}
	defer clear(second)
	if !bytes.Equal(first, second) {
		return c.fail("The two PINs differ, nothing was changed")
	}
	hash, err := pin.Hash(first)
	if err != nil {
		return c.errorf("%v", err)
	}

	argument := "pin"
	if lock {
		argument = "pin-user"
		uid, err := c.UserUID(c.user)
		if err != nil {
			return c.errorf("%v", err)
		}
		gid, err := c.UserGID(c.user)
		if err != nil {
			return c.errorf("%v", err)
		}
		err = pin.SaveUser(c.Paths.PinUserDir, c.user, hash, uid, gid)
		if err != nil {
			return c.errorf("%v", err)
		}
	} else if err := pin.Save(c.Paths.PinDir, c.user, hash); err != nil {
		return c.errorf("%v", err)
	}

	c.printf("\nPIN set for %s, with its wrong tries reset. It is only asked for where pam_howdy.so has the %s argument:\n", c.user, argument)
	c.printf("\n\tauth sufficient pam_howdy.so %s\n\n", argument)
	if lock {
		c.say("This one is for lock screens, which run as you. Its files belong to you, so anything running")
		c.say("as you can read it: it guards against someone at the keyboard, not against malware.")
		c.say("Don't use it for sudo or login managers, which have the root-only PIN.")
	}
	c.printf("%d wrong tries in a row, over any number of face scans, switch it off until you set it again.\n", pin.MaxFails)
	c.say("Keep a root shell open while you try it.")
	return 0
}

func (c *cmd) pinClear(lock bool) int {
	dir, remove := c.Paths.PinDir, pin.Remove
	if lock {
		dir, remove = c.Paths.PinUserDir, pin.RemoveUser
	}
	if !pin.Exists(dir, c.user) {
		return c.fail("%s has no such PIN", c.user)
	}
	if err := remove(dir, c.user); err != nil {
		return c.errorf("%v", err)
	}
	c.say("PIN removed. Services whose PAM line has its argument now fall back to the password.")
	return 0
}

// weak says whether p is a PIN anyone would try first: one digit repeated,
// an ascending or descending run, or a pair repeated
func weak(p []byte) bool {
	same, up, down := true, true, true
	for i := 1; i < len(p); i++ {
		if p[i] != p[0] {
			same = false
		}
		if p[i] != p[i-1]+1 {
			up = false
		}
		if p[i] != p[i-1]-1 {
			down = false
		}
	}
	return same || up || down || bytes.Equal(p[:2], p[2:])
}

// TerminalSecret reads a line from the terminal in without showing it, and
// refuses anything that isn't a terminal, so a PIN never comes from a pipe
// or a script's arguments. The caller clears the slice when it's done.
func TerminalSecret(in *os.File, out io.Writer) func(string) ([]byte, error) {
	return func(prompt string) ([]byte, error) {
		fd := int(in.Fd())
		old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
		if err != nil {
			return nil, errors.New("run this in a terminal, the PIN is typed without echo")
		}
		quiet := *old
		quiet.Lflag &^= unix.ECHO
		if err := unix.IoctlSetTermios(fd, unix.TCSETSF, &quiet); err != nil {
			return nil, err
		}
		defer func() {
			_ = unix.IoctlSetTermios(fd, unix.TCSETSF, old)
			fmt.Fprintln(out)
		}()
		fmt.Fprint(out, prompt)
		line := make([]byte, 0, 8)
		buf := make([]byte, 1)
		for {
			n, err := in.Read(buf)
			if n == 1 {
				if buf[0] == '\n' || buf[0] == '\r' {
					return line, nil
				}
				if len(line) > 64 {
					clear(line)
					return nil, errors.New("that's not a PIN")
				}
				line = append(line, buf[0])
			}
			if err != nil {
				clear(line)
				return nil, err
			}
		}
	}
}
