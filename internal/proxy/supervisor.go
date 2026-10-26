package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/ossyrian/tau/internal/shell"
)

// Docker runs `docker <args>` with optional stdin.
type Docker interface {
	Exec(stdin io.Reader, args ...string) (string, error)
}

// Supervisor starts and reloads the proxy servers inside the container. It
// runs on the host. Secrets travel on the stdin of `docker exec`, never in an
// argument, so they are not visible in a process list.
//
// Every used proxy has a server of its own, run as its own user.
type Supervisor struct {
	D         Docker
	Container string
	Host      Host
}

func (s *Supervisor) exec(stdin io.Reader, user string, args ...string) (string, error) {
	base := []string{"exec"}
	if stdin != nil {
		base = append(base, "-i")
	}
	return s.D.Exec(stdin, append(append(base, "-u", user, s.Container), args...)...)
}

// setupScript makes what the servers need: a user and a socket directory for
// each used proxy, and none for anything else. It prints the name of every
// proxy whose user changed groups: a running server keeps the groups it
// started with, so those servers have to be started again.
func (s *Supervisor) setupScript(used []string) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	b.WriteString("ensure() { # name user uid shared\n")
	b.WriteString("  if ! id -u \"$2\" >/dev/null 2>&1; then\n")
	b.WriteString("    useradd -r -u \"$3\" -g " + PackagesGroup + " -G " + ClientsGroup + " -d /nonexistent -s /usr/sbin/nologin \"$2\"\n")
	b.WriteString("  fi\n")
	b.WriteString("  in=no; id -nG \"$2\" | tr ' ' '\\n' | grep -qx " + SharedGroup + " && in=yes\n")
	b.WriteString("  if [ \"$4\" = shared ] && [ $in = no ]; then usermod -aG " + SharedGroup + " \"$2\"; echo \"$1\"; fi\n")
	b.WriteString("  if [ \"$4\" != shared ] && [ $in = yes ]; then gpasswd -d \"$2\" " + SharedGroup + " >/dev/null; echo \"$1\"; fi\n")
	b.WriteString("  install -d -m 0750 -o \"$2\" -g " + ClientsGroup + " " + SocketDir + "/\"$1\"\n")
	b.WriteString("}\n")
	for _, n := range used {
		shared := "private"
		if s.Host.NeedsSharedGroup(n) {
			shared = "shared"
		}
		fmt.Fprintf(&b, "ensure %s %s %d %s\n", shell.Quote(n), shell.Quote(UserFor(n)), UIDFor(n), shared)
	}
	// Anything else in the socket directory belongs to a proxy that is gone.
	b.WriteString("for d in " + SocketDir + "/*; do\n  [ -d \"$d\" ] || continue\n  n=${d##*/}\n  case \" $keep \" in *\" $n \"*) continue;; esac\n")
	b.WriteString("  pkill -KILL -u \"tx-$n\" 2>/dev/null || true\n  rm -rf \"$d\"\ndone\n")
	keep := strings.Join(used, " ")
	return "keep=" + shell.Quote(keep) + "\n" + b.String()
}

// prepare creates users and directories. It returns the proxies whose servers
// must be started afresh because their groups changed; those are stopped here.
func (s *Supervisor) prepare(used []string) (map[string]bool, error) {
	out, err := s.exec(strings.NewReader(s.setupScript(used)), "root", "sh", "-s")
	if err != nil {
		return nil, fmt.Errorf("prepare proxy users: %w", err)
	}
	changed := map[string]bool{}
	for _, n := range strings.Fields(out) {
		changed[n] = true
		// SIGKILL, not SIGTERM: a server that shuts down gracefully removes its
		// socket path, and could remove the new server's socket. pkill exits 1
		// when nothing matched, which is fine.
		s.exec(nil, "root", "pkill", "-KILL", "-u", UserFor(n))
	}
	return changed, nil
}

// running reports whether a proxy's server answers.
func (s *Supervisor) running(name string) bool {
	_, err := s.exec(nil, UserFor(name), "tau-proxy", "up", name)
	return err == nil
}

// launch starts a server in the background and gives it its document.
// `docker exec -d` does not pass stdin, so the document goes through a named
// pipe instead. The pipe is owned by the server's user and mode 0600, lives in
// the server's own directory, and holds nothing once it is read: the secrets
// never reach a disk or an argument list.
func (s *Supervisor) launch(name string, doc io.Reader) error {
	fifo := path.Join(DirFor(name), "boot")
	user := UserFor(name)
	if _, err := s.exec(nil, user, "sh", "-c", `rm -f "$1" && mkfifo -m 600 "$1"`, "sh", fifo); err != nil {
		return err
	}
	if _, err := s.D.Exec(nil, "exec", "-d", "-u", user, s.Container,
		"sh", "-c", `tau-proxy serve "$1" < "$2"`, "sh", name, fifo); err != nil {
		return err
	}
	_, err := s.exec(doc, user, "sh", "-c", `cat > "$1"`, "sh", fifo)
	return err
}

// plan is the work for one proxy: a document and whether to start the server.
type plan struct {
	name   string
	doc    *bytes.Reader
	launch bool
}

// sync brings the servers of the named proxies up to date. A server that is
// not running is started and is sent fresh credentials. A running one gets a
// reload, which carries fresh credentials only if the name is in refresh.
// Every document is built before anything is changed, so a failed env_from
// leaves all servers as they were.
func (s *Supervisor) sync(only []string, refresh map[string]bool) error {
	if err := s.Host.Check(); err != nil {
		return err
	}
	used := s.Host.Used()
	changed, err := s.prepare(used)
	if err != nil {
		return err
	}
	var plans []plan
	for _, n := range only {
		start := changed[n] || !s.running(n)
		raw, err := s.document(n, start || refresh[n])
		if err != nil {
			return err
		}
		plans = append(plans, plan{n, raw, start})
	}
	for _, p := range plans {
		if p.launch {
			if err := s.launch(p.name, p.doc); err != nil {
				return fmt.Errorf("start the %s proxy server: %w", p.name, err)
			}
			continue
		}
		if _, err := s.exec(p.doc, UserFor(p.name), "tau-proxy", "reload", p.name); err != nil {
			return fmt.Errorf("reload the %s proxy server: %w", p.name, err)
		}
	}
	return nil
}

func (s *Supervisor) document(name string, mint bool) (*bytes.Reader, error) {
	b, err := s.Host.Bootstrap(name, mint)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(raw), nil
}

// Start brings up a server for every used proxy and mints all credentials.
func (s *Supervisor) Start() error {
	used := s.Host.Used()
	all := map[string]bool{}
	for _, n := range used {
		all[n] = true
	}
	return s.sync(used, all)
}

// Reload sends stored variables and the profiles' choices to every server. It
// mints nothing: credentials a server holds stay as they are. A proxy used for
// the first time is started.
func (s *Supervisor) Reload() error { return s.sync(s.Host.Used(), nil) }

// Refresh mints fresh credentials for the named proxies and sends them to
// those servers only. With no names it refreshes every used proxy. Profiles
// that use a proxy get its new credentials; no other server is touched.
func (s *Supervisor) Refresh(names []string) error {
	used := s.Host.Used()
	if len(names) == 0 {
		names = used
	}
	known := map[string]bool{}
	for _, n := range used {
		known[n] = true
	}
	refresh := map[string]bool{}
	for _, n := range names {
		if !known[n] {
			if _, defined := s.Host.Proxies.Defs[n]; defined {
				return fmt.Errorf("no profile uses %q, so it has no server to refresh", n)
			}
			return fmt.Errorf("no proxy %q", n)
		}
		refresh[n] = true
	}
	return s.sync(names, refresh)
}

// InstallShims writes one small script per command the agent types into
// ShimDir. A new proxy needs no rebuild.
func (s *Supervisor) InstallShims() error {
	var script strings.Builder
	script.WriteString("set -e\nrm -rf " + ShimDir + "\nmkdir -p " + ShimDir + "\ncd " + ShimDir + "\n")
	for _, c := range s.Host.Commands() {
		script.WriteString(fmt.Sprintf("cat > %s <<'TAU_SHIM'\n%sTAU_SHIM\nchmod 755 %s\n", shell.Quote(c), ShimScript(c), shell.Quote(c)))
	}
	_, err := s.exec(strings.NewReader(script.String()), "root", "sh", "-s")
	return err
}
