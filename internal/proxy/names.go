package proxy

import (
	"fmt"
	"hash/fnv"
	"path"
	"regexp"
)

// Names of the container users and the directories that hold the sockets.
const (
	// ClientsGroup holds every profile's user. It may connect to the sockets.
	ClientsGroup = "tau-clients"
	ClientsGID   = 1101
	// PackagesGroup may read the hidden packages. Proxy users are in it; profile
	// users are not.
	PackagesGroup = "tau-proxies"
	// SharedGroup reaches the shared workspace.
	SharedGroup = "tau-shared"
	SocketDir   = "/run/tau-proxy"
	// ShimDir holds a small script per command the agent types; it comes first
	// on the agent's PATH.
	ShimDir = "/opt/tau/shims"
	// PackageDir holds the hidden packages. Its mode keeps profile users out.
	PackageDir = "/opt/tau/proxy-packages"
	// ServerPath is the PATH a proxied program runs with when its package gives
	// it none of its own.
	ServerPath = "/usr/local/bin:/usr/bin:/bin"
	// SharedWorkspacePath is where the shared workspace is mounted.
	SharedWorkspacePath = "/srv/shared/workspace"
)

// A proxy name becomes part of a user name, which Debian limits to 32
// characters with no dots, so the pattern is tighter than for other names.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)

// ValidName reports whether name can be a proxy's name.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// UserFor is the container user a proxy's server runs as. Each proxy has its
// own, so one proxy's secrets are not in the memory another can read.
func UserFor(name string) string { return "tx-" + name }

// DirFor is the directory holding a proxy's socket. Only the proxy's user can
// write there, so an agent cannot put its own socket in its place.
func DirFor(name string) string { return path.Join(SocketDir, name) }

// SocketFor is a proxy's socket path.
func SocketFor(name string) string { return path.Join(DirFor(name), "sock") }

// PackagePrefix is where a hidden package installs.
func PackagePrefix(pkg string) string { return path.Join(PackageDir, pkg) }

// UIDFor is a proxy's uid, stable across runs and apart from profile uids
// (2000-21999) and the fixed ones.
func UIDFor(name string) int {
	h := fnv.New32a()
	h.Write([]byte(name))
	return 30000 + int(h.Sum32()%20000)
}

// CheckUIDs fails if two names map to the same uid.
func CheckUIDs(names []string) error {
	seen := map[int]string{}
	for _, n := range names {
		u := UIDFor(n)
		if other, dup := seen[u]; dup && other != n {
			return fmt.Errorf("proxies %q and %q map to the same uid %d: rename one", other, n, u)
		}
		seen[u] = n
	}
	return nil
}
