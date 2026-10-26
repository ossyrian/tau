// Package build turns a resolved install plan into Dockerfile text.
package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"maps"
	"slices"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/shell"
)

// MarkerDir holds one file per installed package inside the image. Each file
// contains the package's Hash, so a later check can tell whether the image
// matches the current config.
const MarkerDir = "/etc/tau/installed"

// renderFor writes the packages of one user. Their markers go in markerDir,
// owned by user; an "as: user" package runs as user.
func renderFor(b *strings.Builder, user, markerDir string, entries []config.Entry) error {
	fmt.Fprintf(b, "USER root\nRUN install -d -o %s -g %s %s\n", user, user, markerDir)
	for _, e := range entries {
		block, err := renderEntry(e, user, markerDir)
		if err != nil {
			return fmt.Errorf("package %q: %w", e.Name, err)
		}
		b.WriteString(block)
	}
	return nil
}

func renderEntry(e config.Entry, userName, markerDir string) (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	if err := checkText(e); err != nil {
		return "", err
	}
	user := "root"
	if e.As == config.AsUser {
		user = userName
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# package: %s\nUSER %s\n", e.Name, user)
	for _, k := range slices.Sorted(maps.Keys(e.Env)) {
		fmt.Fprintf(&b, "ENV %s=%s\n", k, dockerQuote(e.Env[k]))
	}

	body := renderBody(e, markerDir)
	delim := delimiterFor(body)
	fmt.Fprintf(&b, "RUN bash -eo pipefail <<'%s'\n%s%s\n", delim, body, delim)
	return b.String(), nil
}

// renderBody is the shell program for one package.
func renderBody(e config.Entry, markerDir string) string {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(e.Vars)) {
		fmt.Fprintf(&b, "export %s=%s\n", k, shell.Quote(e.Vars[k]))
	}
	install := installScripts(e)
	if e.Check != "" {
		// The closing paren is on its own line, so a check that ends in a
		// comment cannot swallow it.
		fmt.Fprintf(&b, "if (\n%s\n) >/dev/null 2>&1; then\necho \"tau: %s: already installed, skipping\"\nelse\n%sfi\n",
			e.Check, e.Name, install)
	} else {
		b.WriteString(install)
	}
	fmt.Fprintf(&b, "printf '%%s\\n' '%s' > %s/%s\n", Hash(e), markerDir, e.Name)
	return b.String()
}

// installScripts runs each script in its own subshell, so one script's
// directory changes and variables do not leak into the next, while set -e still applies.
func installScripts(e config.Entry) string {
	scripts := e.Script
	if len(e.Apt) > 0 {
		scripts = []string{aptScript(e.Apt)}
	}
	var b strings.Builder
	for _, s := range scripts {
		b.WriteString("(\n" + strings.TrimRight(s, "\n") + "\n)\n")
	}
	return b.String()
}

func aptScript(pkgs []string) string {
	return "export DEBIAN_FRONTEND=noninteractive\n" +
		"apt-get update\n" +
		"apt-get install -y --no-install-recommends " + strings.Join(pkgs, " ") + "\n" +
		"rm -rf /var/lib/apt/lists/*"
}

// delimiterFor picks a heredoc delimiter that appears nowhere in body.
func delimiterFor(body string) string {
	d := "TAU_EOS"
	for i := 1; strings.Contains(body, d); i++ {
		d = "TAU_EOS_" + strconv.Itoa(i)
	}
	return d
}

// checkText rejects text that cannot be carried safely to the shell or into ENV.
func checkText(e config.Entry) error {
	for _, s := range e.Script {
		if strings.ContainsRune(s, 0) {
			return fmt.Errorf("script contains a NUL byte")
		}
	}
	if strings.ContainsRune(e.Check, 0) {
		return fmt.Errorf("check contains a NUL byte")
	}
	for k, v := range e.Vars {
		if strings.ContainsRune(v, 0) {
			return fmt.Errorf("vars.%s contains a NUL byte", k)
		}
	}
	for k, v := range e.Env {
		if strings.ContainsAny(v, "\x00\n\r") {
			return fmt.Errorf("env.%s must be a single line without NUL bytes", k)
		}
	}
	return nil
}

// dockerQuote double-quotes s for a Dockerfile ENV value, so $ stays literal.
func dockerQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`)
	return `"` + r.Replace(s) + `"`
}

// Hash identifies what an entry installs: its scripts or apt list, check, user,
// env and vars. The name and needs are left out, because changing them does not
// change what is installed. Fields are length-prefixed, so values cannot run
// together and match a different entry.
func Hash(e config.Entry) string {
	h := sha256.New()
	field := func(tag, s string) {
		fmt.Fprintf(h, "%s%d:%s;", tag, len(s), s)
	}
	list := func(tag string, l []string) {
		fmt.Fprintf(h, "%s[%d]", tag, len(l))
		for _, s := range l {
			field("i", s)
		}
	}
	m := func(tag string, kv map[string]string) {
		fmt.Fprintf(h, "%s{%d}", tag, len(kv))
		for _, k := range slices.Sorted(maps.Keys(kv)) {
			field("k", k)
			field("v", kv[k])
		}
	}
	as := e.As
	if as == "" {
		as = config.AsRoot
	}
	field("as", as)
	field("check", e.Check)
	list("script", e.Script)
	list("apt", e.Apt)
	m("env", e.Env)
	m("vars", e.Vars)
	return hex.EncodeToString(h.Sum(nil))
}

// ConfigHash identifies a whole install plan: every entry's name and Hash, in order.
func ConfigHash(entries []config.Entry) string {
	h := sha256.New()
	fmt.Fprintf(h, "n=%d;", len(entries))
	for _, e := range entries {
		fmt.Fprintf(h, "%d:%s=%s;", len(e.Name), e.Name, Hash(e))
	}
	return hex.EncodeToString(h.Sum(nil))
}
