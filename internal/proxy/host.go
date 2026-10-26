package proxy

import (
	"fmt"
	"path"
	"strconv"

	"maps"
	"slices"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/profile"
)

// Member is one profile as the proxy servers see it.
type Member struct {
	Profile profile.Profile
	// Active names the proxies the profile may call.
	Active []string
	// SharedWorkspace is true when the profile uses the shared workspace.
	SharedWorkspace bool
}

// Host works out what to tell the servers. It runs on the host.
type Host struct {
	Proxies config.Proxies
	Members []Member
	Secrets *Secrets
}

// Used lists, in order, the proxies at least one profile has chosen. Only
// these get a server, so a proxy nobody uses holds no secrets in memory.
func (h Host) Used() []string {
	seen := map[string]bool{}
	for _, m := range h.Members {
		for _, n := range m.Active {
			if _, ok := h.Proxies.Defs[n]; ok {
				seen[n] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// Check makes sure every defined proxy can have a server: a name that works as
// a user name, a package that exists, and a uid of its own.
func (h Host) Check() error {
	names := h.Proxies.Names()
	for _, n := range names {
		if !ValidName(n) {
			return fmt.Errorf("proxy %q cannot have a server: the name must start with a letter, then lowercase letters, digits or -", n)
		}
		def := h.Proxies.Defs[n]
		if def.Package != "" {
			if _, ok := h.Proxies.Packages[def.Package]; !ok {
				return fmt.Errorf("proxy %q: package %q is not declared", n, def.Package)
			}
		}
	}
	return CheckUIDs(names)
}

// NeedsSharedGroup is true when the proxy's user must reach the shared workspace.
func (h Host) NeedsSharedGroup(name string) bool {
	return h.Proxies.Defs[name].InAgentDirectory()
}

// Bootstrap builds the document for one proxy's server: the proxy with its
// secrets, and which uid may call it. With mint, the proxy's env_from runs and
// its result is sent as the minted credentials. Without it Minted stays nil,
// which tells a running server to keep the credentials it holds.
func (h Host) Bootstrap(name string, mint bool) (Bootstrap, error) {
	def, ok := h.Proxies.Defs[name]
	if !ok {
		return Bootstrap{}, fmt.Errorf("no proxy %q", name)
	}
	spec, err := h.spec(name, def, mint)
	if err != nil {
		return Bootstrap{}, err
	}
	b := Bootstrap{Proxies: map[string]Spec{name: spec}, Users: map[string][]string{}, Roots: map[string][]string{}}
	for _, m := range h.Members {
		if !hasName(m.Active, name) {
			continue
		}
		if def.InAgentDirectory() && !m.SharedWorkspace {
			continue // its workspace is private to it, so the proxy cannot reach it
		}
		uid := strconv.Itoa(m.Profile.UID)
		b.Users[uid] = []string{name}
		if def.InAgentDirectory() {
			b.Roots[uid] = []string{SharedWorkspacePath}
		}
	}
	return b, nil
}

func (h Host) spec(name string, def config.Proxy, mint bool) (Spec, error) {
	stored, err := h.Secrets.Vars(name)
	if err != nil {
		return Spec{}, fmt.Errorf("proxy %q: %w", name, err)
	}
	s := Spec{
		AgentCommand: def.AgentCommand, Description: def.Description, Deny: def.Deny,
		InWorkspace: def.InAgentDirectory(), Env: MergeEnv(stored, nil),
	}
	if def.Package != "" {
		prefix := PackagePrefix(def.Package)
		s.Exec = path.Join(prefix, def.Binary)
		s.Path = path.Join(prefix, "bin") + ":" + ServerPath
	} else {
		s.Exec = def.SystemBinary
	}
	if mint {
		minted, err := h.Secrets.Mint(def.EnvFrom)
		if err != nil {
			return Spec{}, fmt.Errorf("proxy %q: %w", name, err)
		}
		s.Minted = MergeEnv(minted, nil)
	}
	return s, nil
}

// Commands lists, once each, the commands the agent types. Each gets a shim.
func (h Host) Commands() []string {
	seen := map[string]bool{}
	for _, d := range h.Proxies.Defs {
		seen[d.AgentCommand] = true
	}
	return slices.Sorted(maps.Keys(seen))
}

// ShimScript is the script that stands in for a command in the agent's PATH.
// It passes its arguments through untouched.
func ShimScript(command string) string {
	return "#!/bin/sh\nexec tau-proxy call " + command + " -- \"$@\"\n"
}
