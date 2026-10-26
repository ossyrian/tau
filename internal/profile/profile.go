// Package profile names the profiles tau can run and says where each keeps its
// files and which OS user it is inside the container.
//
// The default profile uses the tau home itself (~/.tau/config.yaml and so on).
// Any other profile P uses ~/.tau/profiles/P with the same layout, so every
// path is "the profile's directory plus a fixed name".
package profile

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// Default is the profile that uses the tau home directly.
const Default = "default"

// DefaultUser is the container user of the default profile.
const DefaultUser = "pi"

const (
	uidBase  = 2000  // first uid a named profile can get
	uidRange = 20000 // named profiles get uids in [uidBase, uidBase+uidRange)
)

// A name becomes a directory and, with a prefix, a Linux user name (32 chars).
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)

// reserved names would collide with users that exist in the image.
var reserved = map[string]bool{"root": true, "pi": true}

// Profile is one profile.
type Profile struct {
	Name string
	Dir  string // host directory holding the profile's files
	User string // container user name
	UID  int
}

// Home is the profile user's home directory in the container.
func (p Profile) Home() string { return "/home/" + p.User }

// Paths on the host. They are the same for every profile, below Dir.
func (p Profile) ConfigFile() string  { return filepath.Join(p.Dir, "config.yaml") }
func (p Profile) RuntimeFile() string { return filepath.Join(p.Dir, "config.runtime.yaml") }
func (p Profile) PiDir() string       { return filepath.Join(p.Dir, "pi") }
func (p Profile) EnvFile() string     { return filepath.Join(p.Dir, ".env") }
func (p Profile) Zshrc() string       { return filepath.Join(p.Dir, "zshrc") }
func (p Profile) Vimrc() string       { return filepath.Join(p.Dir, "vimrc") }
func (p Profile) GitConfig() string   { return filepath.Join(p.Dir, ".gitconfig") }
func (p Profile) Scripts() string     { return filepath.Join(p.Dir, "scripts") }
func (p Profile) Share() string       { return filepath.Join(p.Dir, "share") }
func (p Profile) Treehouse() string   { return filepath.Join(p.Dir, "treehouse") }
func (p Profile) Workspace() string   { return filepath.Join(p.Dir, "workspace") }

// Set is the profiles under one tau home.
type Set struct{ Root string }

func (s Set) profilesDir() string { return filepath.Join(s.Root, "profiles") }

// SecretsDir holds one <proxy>.env file per proxy. Proxies are shared by
// every profile, so their secrets are too.
func (s Set) SecretsDir() string { return filepath.Join(s.Root, "secrets") }

// Get returns the named profile without checking that it exists. An empty
// name means the default profile.
func (s Set) Get(name string) (Profile, error) {
	if name == "" || name == Default {
		return Profile{Name: Default, Dir: s.Root, User: DefaultUser, UID: 1000}, nil
	}
	if !namePattern.MatchString(name) {
		return Profile{}, fmt.Errorf("profile name must be lowercase letters, digits and - (starting with a letter, at most 24): %q", name)
	}
	if reserved[name] {
		return Profile{}, fmt.Errorf("profile name %q is reserved", name)
	}
	return Profile{Name: name, Dir: filepath.Join(s.profilesDir(), name), User: "p-" + name, UID: uidFor(name)}, nil
}

// uidFor maps a name to a stable uid, so a profile keeps its files' owner
// across rebuilds and hosts.
func uidFor(name string) int {
	h := fnv.New32a()
	h.Write([]byte(name))
	return uidBase + int(h.Sum32()%uidRange)
}

// Select returns the named profile and requires it to exist.
func (s Set) Select(name string) (Profile, error) {
	p, err := s.Get(name)
	if err != nil {
		return Profile{}, err
	}
	if p.Name == Default {
		return p, nil
	}
	if st, err := os.Stat(p.Dir); err != nil || !st.IsDir() {
		return Profile{}, fmt.Errorf("no profile %q (tau profiles new %s creates it)", name, name)
	}
	return p, nil
}

// List returns the default profile, then every named profile in name order.
// A directory whose name is not a valid profile name is ignored. Two profiles
// that map to the same uid are an error: they would share files.
func (s Set) List() ([]Profile, error) {
	def, _ := s.Get(Default)
	out := []Profile{def}
	entries, err := os.ReadDir(s.profilesDir())
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	byUID := map[int]string{}
	for _, n := range names {
		p, err := s.Get(n)
		if err != nil {
			continue
		}
		if other, dup := byUID[p.UID]; dup {
			return nil, fmt.Errorf("profiles %q and %q map to the same uid %d: rename one", other, n, p.UID)
		}
		byUID[p.UID] = n
		out = append(out, p)
	}
	return out, nil
}

// Create makes the profile's directories. The default profile always exists.
func (s Set) Create(name string) (Profile, error) {
	if name == "" || name == Default {
		return Profile{}, errors.New("the default profile always exists")
	}
	p, err := s.Get(name)
	if err != nil {
		return Profile{}, err
	}
	if _, err := os.Stat(p.Dir); err == nil {
		return Profile{}, fmt.Errorf("profile %q already exists", name)
	}
	existing, err := s.List()
	if err != nil {
		return Profile{}, err
	}
	for _, e := range existing {
		if e.UID == p.UID {
			return Profile{}, fmt.Errorf("profile %q would share uid %d with %q: pick another name", name, p.UID, e.Name)
		}
	}
	if err := os.MkdirAll(p.PiDir(), 0o755); err != nil {
		return Profile{}, err
	}
	return p, nil
}
