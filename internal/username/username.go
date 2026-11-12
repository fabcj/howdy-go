// Package username says which user names may go into a path under the
// models, PIN and sface directories: what useradd allows, and what the PAM
// module's safe_username checks (pam-pin.patch).
package username

import "regexp"

var re = regexp.MustCompile(`^[a-z_][a-z0-9_-]*[$]?$`)

// Valid checks a username before it goes into a path
func Valid(user string) bool {
	return len(user) <= 32 && re.MatchString(user)
}
