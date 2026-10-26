// Package shell quotes text for sh and bash.
package shell

import "strings"

// Quote single-quotes s. It is safe for any text without NUL.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
