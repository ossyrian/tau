// Package container creates, stops and inspects the tau container. It builds
// the `docker run` arguments from host-side state.
package container

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	portPattern   = regexp.MustCompile(`^(\d{1,5})(?:-(\d{1,5}))?$`)
	wsNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// PortArgs publishes each port or range in spec (comma-separated, e.g.
// "5173,8400-8409") on the host's loopback only. Entries that are not a valid
// port or range are dropped, so spec cannot add other docker syntax.
func PortArgs(spec string) []string {
	var args []string
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if !validPorts(part) {
			continue
		}
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:%s:%s", part, part))
	}
	return args
}

func validPorts(s string) bool {
	m := portPattern.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	lo, _ := strconv.Atoi(m[1])
	hi := lo
	if m[2] != "" {
		hi, _ = strconv.Atoi(m[2])
	}
	return lo >= 1 && hi <= 65535 && lo <= hi
}

// SkillMount is one skill directory to mount into Pi.
type SkillMount struct{ Name, Path string }

// SkillArgs returns a -v argument for each skill, mounted into the Pi config
// under home.
func SkillArgs(home string, mounts []SkillMount) []string {
	var args []string
	for _, m := range mounts {
		args = append(args, "-v", m.Path+":"+home+"/.pi/agent/skills/"+m.Name)
	}
	return args
}

// ExpandHome turns a leading "~" into home. Nothing else is expanded.
func ExpandHome(p, home string) string {
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	}
	return p
}

// LiveWorkspaceMounts returns a -v argument for each live workspace (name to
// host directory), mounting it at dest/<name>, in name order. A source that
// starts with "~" is taken from home. A name that is not one safe path
// component, and a source that is not a directory, are skipped with a warning,
// so a config entry cannot mount outside dest.
func LiveWorkspaceMounts(live map[string]string, home, dest string, warn io.Writer) []string {
	names := make([]string, 0, len(live))
	for n := range live {
		names = append(names, n)
	}
	sort.Strings(names)
	var args []string
	for _, name := range names {
		if !wsNamePattern.MatchString(name) {
			fmt.Fprintf(warn, "tau: live workspace has an unsafe name, skipping: %q\n", name)
			continue
		}
		src := ExpandHome(live[name], home)
		if st, err := os.Stat(src); err != nil || !st.IsDir() {
			fmt.Fprintf(warn, "tau: live workspace source not found, skipping: %s -> %s\n", name, src)
			continue
		}
		args = append(args, "-v", src+":"+dest+"/"+name)
	}
	return args
}
