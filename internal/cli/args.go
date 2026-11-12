// Package cli is the howdy command, a port of Howdy's cli.py and cli/*.py
package cli

import (
	"fmt"
	"slices"
	"strings"
)

var commands = []string{"add", "clear", "config", "disable", "list", "pin", "remove", "set", "snapshot", "test", "version"}

const usage = "usage: howdy [-U USER] [--plain] [-h] [-y] command [arguments...]\n"

const help = usage + `
Command line interface for Howdy face authentication.

positional arguments:
  command          The command option to execute, can be one of the following:
                   add, clear, config, disable, list, pin, remove, snapshot,
                   set, test or version.
  arguments        Optional arguments for the add, disable, pin, remove
                   and set commands.

options:
  -U, --user USER  Set the user account to use.
  -y               Skip all questions.
  --plain          Print machine-friendly output.
  -h, --help       Show this help message and exit.

For support please visit
https://github.com/boltgolt/howdy
`

type args struct {
	command   string
	arguments []string
	user      string
	yes       bool
	plain     bool
	help      bool
}

// usageError is what argparse prints before exiting with 2
type usageError string

func (e usageError) Error() string { return string(e) }

// parse reads argv the way argparse does for cli.py: options anywhere, the
// command and its arguments as one run of positionals, or the arguments in
// a second run if the first one held only the command
func parse(argv []string, defaultUser string) (*args, error) {
	a := &args{user: defaultUser}
	var runs [][]string
	var cur []string
	var unknown []string
	endFlags := false

	flush := func() {
		if len(cur) > 0 {
			runs = append(runs, cur)
			cur = nil
		}
	}

	for i := 0; i < len(argv); i++ {
		t := argv[i]
		if endFlags || t == "-" || !strings.HasPrefix(t, "-") {
			cur = append(cur, t)
			continue
		}
		flush()
		switch {
		case t == "--":
			endFlags = true
		case t == "-h" || t == "--help":
			a.help = true
			return a, nil
		case t == "-y":
			a.yes = true
		case t == "--plain":
			a.plain = true
		case t == "-U" || t == "--user":
			if i+1 >= len(argv) || (strings.HasPrefix(argv[i+1], "-") && argv[i+1] != "-") {
				return nil, usageError("argument -U/--user: expected one argument")
			}
			i++
			a.user = argv[i]
		case strings.HasPrefix(t, "--user="):
			a.user = strings.TrimPrefix(t, "--user=")
		case strings.HasPrefix(t, "-U"):
			a.user = strings.TrimPrefix(t, "-U")
		default:
			unknown = append(unknown, t)
		}
	}
	flush()

	if len(runs) == 0 {
		return nil, usageError("the following arguments are required: command")
	}
	a.command = runs[0][0]
	if !slices.Contains(commands, a.command) {
		quoted := make([]string, len(commands))
		for i, c := range commands {
			quoted[i] = "'" + c + "'"
		}
		return nil, usageError(fmt.Sprintf("argument command: invalid choice: '%s' (choose from %s)", a.command, strings.Join(quoted, ", ")))
	}
	rest := runs[1:]
	if len(runs[0]) > 1 {
		a.arguments = runs[0][1:]
	} else if len(rest) > 0 {
		a.arguments = rest[0]
		rest = rest[1:]
	}
	for _, r := range rest {
		unknown = append(unknown, r...)
	}
	if len(unknown) > 0 {
		return nil, usageError("unrecognized arguments: " + strings.Join(unknown, " "))
	}
	return a, nil
}
