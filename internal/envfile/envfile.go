// Package envfile reads and edits the .env file that tau passes to the
// container with `docker run --env-file`. That format has no quoting: a value
// is everything after the first "=", so quotes are kept as typed.
package envfile

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Var is one KEY=value line.
type Var struct{ Key, Value string }

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseLine returns the variable on a line, if it holds one.
func parseLine(line string) (Var, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return Var{}, false
	}
	k, v, ok := strings.Cut(line, "=")
	k = strings.TrimSpace(k)
	if !ok || !keyPattern.MatchString(k) {
		return Var{}, false
	}
	return Var{k, strings.TrimSpace(v)}, true
}

// List returns the variables in file order. A repeated key keeps the position
// of its first use and the value of its last, as Docker does. A missing file
// has none.
func List(path string) ([]Var, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var vars []Var
	at := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		v, ok := parseLine(line)
		if !ok {
			continue
		}
		if i, seen := at[v.Key]; seen {
			vars[i].Value = v.Value
			continue
		}
		at[v.Key] = len(vars)
		vars = append(vars, v)
	}
	return vars, nil
}

// Set gives key the value. It replaces the key's first line in place, drops
// repeats, and keeps comments. A new key goes at the end.
func Set(path, key, value string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("invalid name %q: use letters, digits and _, not starting with a digit", key)
	}
	if strings.ContainsAny(value, "\r\n\x00") || !utf8.ValidString(value) {
		return errors.New("a value must be one line of text")
	}
	return edit(path, func(lines []string) []string {
		out := make([]string, 0, len(lines)+1)
		done := false
		for _, l := range lines {
			if v, ok := parseLine(l); ok && v.Key == key {
				if !done {
					out = append(out, key+"="+value)
					done = true
				}
				continue
			}
			out = append(out, l)
		}
		if !done {
			out = append(out, key+"="+value)
		}
		return out
	})
}

// Unset removes every line that sets key. It reports whether any did.
func Unset(path, key string) (bool, error) {
	removed := false
	err := edit(path, func(lines []string) []string {
		out := make([]string, 0, len(lines))
		for _, l := range lines {
			if v, ok := parseLine(l); ok && v.Key == key {
				removed = true
				continue
			}
			out = append(out, l)
		}
		return out
	})
	if !removed {
		return false, err // nothing changed, and a missing file stays missing
	}
	return true, err
}

// edit applies fn to the file's lines and writes the result. It does not
// create the file when fn would leave it unchanged and the file is absent.
func edit(path string, fn func([]string) []string) error {
	data, err := os.ReadFile(path)
	mode := os.FileMode(0o600) // secrets live here
	switch {
	case errors.Is(err, os.ErrNotExist):
		data = nil
	case err != nil:
		return err
	default:
		if st, err := os.Stat(path); err == nil {
			mode = st.Mode().Perm()
		}
	}
	text := strings.TrimSuffix(string(data), "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(text, "\n")
	}
	out := fn(lines)
	if len(out) == 0 && len(data) == 0 {
		return nil
	}
	body := ""
	if len(out) > 0 {
		body = strings.Join(out, "\n") + "\n"
	}
	return os.WriteFile(path, []byte(body), mode)
}

// Mask hides all of a value but its last four characters, so a token can be
// recognized without being shown. A value of four characters or fewer is
// hidden completely.
func Mask(v string) string {
	r := []rune(v)
	if len(r) <= 4 {
		return strings.Repeat("•", len(r))
	}
	return strings.Repeat("•", len(r)-4) + string(r[len(r)-4:])
}

// secretWords are name parts that mark a credential. A name is split on "_",
// so KEYBOARD and MONKEY do not match KEY.
var secretWords = map[string]bool{
	"TOKEN": true, "SECRET": true, "PASSWORD": true, "PASSWD": true, "KEY": true,
	"APIKEY": true, "CREDENTIAL": true, "CREDENTIALS": true, "PRIVATE": true,
}

// LooksSecret reports whether a variable name looks like a credential. The
// agent can read every variable in a profile's env, so credentials belong in a
// proxy instead; callers use this to say so.
func LooksSecret(name string) bool {
	for _, part := range strings.Split(strings.ToUpper(name), "_") {
		if secretWords[part] {
			return true
		}
	}
	return false
}
