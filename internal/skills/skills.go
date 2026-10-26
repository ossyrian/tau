// Package skills decides which skills the tau container loads into Pi.
//
// config.yaml holds the settings:
//
//	skills:
//	  paths: [~/.agents/skills]  # directories searched for skills
//	  active: [voice, prune]     # skills loaded into the agents
//
// A skill is a subdirectory of a path that holds a SKILL.md. Tau finds every
// skill in the paths but loads only the active ones. An active entry is a skill
// name or the path of a skill directory. Docker cannot change the mounts of a
// running container, so changes apply on the next start or restart.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ossyrian/tau/internal/config"
	"github.com/sahilm/fuzzy"
)

// DestFor is the container directory that holds the mounted skills of the
// user whose home is home.
func DestFor(home string) string { return home + "/.pi/agent/skills/" }

// Dest is DestFor the default profile's home.
const Dest = "/home/pi/.pi/agent/skills/"

// State describes how a skill relates to the container.
type State string

const (
	Inactive   State = "inactive"   // found, not active
	Synced     State = "synced"     // active and mounted
	Pending    State = "pending"    // active, not mounted yet: restart to apply
	Stale      State = "stale"      // mounted, no longer active: restart to apply
	Missing    State = "missing"    // active, but no skill directory is found
	Configured State = "configured" // active; the container is not running
)

// Skill is one entry in the list.
type Skill struct {
	Name, Path string
	Active     bool
	State      State
}

// Mount is a host directory to mount as one skill. Path is the real directory,
// with symlinks resolved. Shown is where the skill was found, which is what a
// person recognises.
type Mount struct{ Name, Path, Shown string }

// PathInfo describes one entry of skills.paths.
type PathInfo struct {
	Path   string // as written in config.yaml
	Found  bool   // the directory exists
	Skills int    // how many skills it holds
}

// Store reads and edits the skills section of config.yaml.
type Store struct {
	Dir  string // directory of the profile's config.yaml
	Home string // host home, for ~
	// Skills are mounted under this container directory. Empty means Dest.
	Dest string
	// Mounted returns the container's skill mounts (destination -> host
	// source) and whether the container is running.
	Mounted func() (map[string]string, bool, error)
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func (s *Store) dest() string {
	if s.Dest == "" {
		return Dest
	}
	return s.Dest
}

func (s *Store) expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = filepath.Join(s.Home, p[1:])
	}
	return filepath.Clean(p)
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func hasSkillFile(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil && st.Mode().IsRegular()
}

// real resolves symlinks, so two spellings of one directory compare equal.
func real(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func isPathLike(x string) bool {
	return x == "~" || strings.HasPrefix(x, "~/") || filepath.IsAbs(x)
}

func unsafePath(p string) bool { return strings.ContainsAny(p, ":\n\r") }

func sameDir(a, b string) bool { return a == b || real(a) == real(b) }

func (s *Store) config() (config.Skills, error) { return config.LoadSkills(s.Dir) }

// discovered lists the skills in the paths. The first path to hold a name wins.
func (s *Store) discovered(paths []string) []Mount {
	var out []Mount
	seen := map[string]bool{}
	for _, p := range paths {
		entries, err := os.ReadDir(s.expand(p))
		if err != nil {
			continue
		}
		for _, e := range entries { // ReadDir sorts by name
			name := e.Name()
			dir := filepath.Join(s.expand(p), name)
			if !safeName.MatchString(name) || seen[name] || !isDir(dir) || !hasSkillFile(dir) {
				continue
			}
			seen[name] = true
			out = append(out, Mount{name, real(dir), dir})
		}
	}
	return out
}

// resolve maps one active entry to a skill. ok is false when no skill is found.
func (s *Store) resolve(entry string, found []Mount) (m Mount, ok bool) {
	if !isPathLike(entry) {
		for _, f := range found {
			if f.Name == entry {
				return f, true
			}
		}
		return Mount{Name: entry}, false
	}
	dir := s.expand(entry)
	m = Mount{Name: filepath.Base(dir), Path: real(dir), Shown: dir}
	return m, isDir(dir) && hasSkillFile(dir) && safeName.MatchString(m.Name) && !unsafePath(m.Path)
}

// Mounts returns what the container must mount: the active skills that exist.
// Active entries that resolve to nothing, or that repeat a name, come back as
// warnings.
func (s *Store) Mounts() (mounts []Mount, warnings []string, err error) {
	cfg, err := s.config()
	if err != nil {
		return nil, nil, err
	}
	found := s.discovered(cfg.Paths)
	names := map[string]string{}
	for _, entry := range cfg.Active {
		m, ok := s.resolve(entry, found)
		switch {
		case !ok:
			warnings = append(warnings, fmt.Sprintf("active skill not found, skipping: %s", entry))
		case names[m.Name] != "":
			warnings = append(warnings, fmt.Sprintf("two active skills are named %q, skipping %s", m.Name, entry))
		default:
			names[m.Name] = m.Path
			mounts = append(mounts, m)
		}
	}
	return mounts, warnings, nil
}

// List returns every skill found in the paths, then active skills found only
// by path, then active entries that resolve to nothing, then skills the
// container still mounts but that are no longer active.
func (s *Store) List() ([]Skill, error) {
	cfg, err := s.config()
	if err != nil {
		return nil, err
	}
	mounted, running, err := s.Mounted()
	if err != nil {
		return nil, err
	}
	found := s.discovered(cfg.Paths)

	active := map[string]Mount{} // by name
	var lost []string
	for _, entry := range cfg.Active {
		m, ok := s.resolve(entry, found)
		if !ok {
			lost = append(lost, entry)
			continue
		}
		if _, dup := active[m.Name]; !dup {
			active[m.Name] = m
		}
	}

	state := func(m Mount) State {
		switch {
		case !running:
			return Configured
		case mounted[s.dest()+m.Name] != "" && sameDir(mounted[s.dest()+m.Name], m.Path):
			return Synced
		}
		return Pending
	}

	var out []Skill
	listed := map[string]bool{}
	for _, f := range found {
		listed[f.Name] = true
		if a, ok := active[f.Name]; ok {
			out = append(out, Skill{f.Name, f.Shown, true, state(a)})
		} else {
			out = append(out, Skill{f.Name, f.Shown, false, Inactive})
		}
	}
	// Active by path, from outside every search path.
	var extra []string
	for name := range active {
		if !listed[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		listed[name] = true
		out = append(out, Skill{name, active[name].Shown, true, state(active[name])})
	}
	for _, entry := range lost {
		name, path := entry, ""
		if isPathLike(entry) {
			path = s.expand(entry)
			name = filepath.Base(path)
		}
		out = append(out, Skill{name, path, true, Missing})
	}

	dests := make([]string, 0, len(mounted))
	for dest := range mounted {
		dests = append(dests, dest)
	}
	sort.Strings(dests)
	for _, dest := range dests {
		name, ok := strings.CutPrefix(dest, s.dest())
		if !ok || name == "" || strings.Contains(name, "/") || listed[name] {
			continue // not a skill mount, or already listed
		}
		out = append(out, Skill{name, mounted[dest], false, Stale})
	}
	return out, nil
}

// Drift counts skills whose state needs a restart to settle.
func (s *Store) Drift() (int, error) {
	l, err := s.List()
	n := 0
	for _, k := range l {
		if k.State == Pending || k.State == Stale {
			n++
		}
	}
	return n, err
}

// Activate loads a skill, given by name or by the path of its directory. It
// returns the skill's name.
func (s *Store) Activate(nameOrPath string) (string, error) {
	cfg, err := s.config()
	if err != nil {
		return "", err
	}
	found := s.discovered(cfg.Paths)
	entry := nameOrPath
	m, ok := s.resolve(entry, found)
	if !ok {
		if !isPathLike(entry) {
			return "", fmt.Errorf("no skill named %q (tau skills list shows what is found)", entry)
		}
		dir := s.expand(entry)
		switch {
		case unsafePath(dir):
			return "", fmt.Errorf("path may not contain ':' or a newline: %q", entry)
		case !isDir(dir):
			return "", fmt.Errorf("not a directory: %s", dir)
		case !hasSkillFile(dir):
			return "", fmt.Errorf("%s has no SKILL.md", dir)
		}
		return "", fmt.Errorf("%q is not a safe skill name", m.Name)
	}
	// A path to a skill the search paths already hold is stored as its name.
	if isPathLike(entry) {
		for _, f := range found {
			if sameDir(f.Path, m.Path) {
				entry = f.Name
			}
		}
	}
	for _, a := range cfg.Active {
		am, aok := s.resolve(a, found)
		switch {
		case aok && sameDir(am.Path, m.Path):
			return "", fmt.Errorf("already active: %s", m.Name)
		case aok && am.Name == m.Name:
			return "", fmt.Errorf("a skill named %q is already active (%s)", m.Name, am.Path)
		}
	}
	cfg.Active = append(cfg.Active, entry)
	return m.Name, config.SaveSkills(s.Dir, cfg)
}

// Deactivate stops loading a skill, given by name or path. The directory on
// the host is never touched.
func (s *Store) Deactivate(nameOrPath string) (string, error) {
	cfg, err := s.config()
	if err != nil {
		return "", err
	}
	found := s.discovered(cfg.Paths)
	want, wantOK := s.resolve(nameOrPath, found)
	for i, a := range cfg.Active {
		m, ok := s.resolve(a, found)
		hit := a == nameOrPath ||
			(ok && wantOK && sameDir(m.Path, want.Path)) ||
			(isPathLike(a) && isPathLike(nameOrPath) && s.expand(a) == s.expand(nameOrPath)) ||
			(!isPathLike(nameOrPath) && m.Name == nameOrPath)
		if !hit {
			continue
		}
		cfg.Active = append(cfg.Active[:i:i], cfg.Active[i+1:]...)
		return m.Name, config.SaveSkills(s.Dir, cfg)
	}
	return "", fmt.Errorf("skill %q is not active", nameOrPath)
}

// Paths lists skills.paths with what each holds.
func (s *Store) Paths() ([]PathInfo, error) {
	cfg, err := s.config()
	if err != nil {
		return nil, err
	}
	out := make([]PathInfo, len(cfg.Paths))
	for i, p := range cfg.Paths {
		out[i] = PathInfo{Path: p, Found: isDir(s.expand(p)), Skills: len(s.discovered([]string{p}))}
	}
	return out, nil
}

func (s *Store) checkPath(p string) error {
	if !isPathLike(p) {
		return fmt.Errorf("give an absolute path or one starting with ~, got %q", p)
	}
	if unsafePath(s.expand(p)) {
		return fmt.Errorf("path may not contain ':' or a newline: %q", p)
	}
	if !isDir(s.expand(p)) {
		return fmt.Errorf("not a directory: %s", s.expand(p))
	}
	return nil
}

// AddPath makes tau search a directory for skills.
func (s *Store) AddPath(p string) error {
	if err := s.checkPath(p); err != nil {
		return err
	}
	cfg, err := s.config()
	if err != nil {
		return err
	}
	for _, q := range cfg.Paths {
		if s.expand(q) == s.expand(p) {
			return fmt.Errorf("already a skill path: %s", q)
		}
	}
	cfg.Paths = append(cfg.Paths, p)
	return config.SaveSkills(s.Dir, cfg)
}

// RemovePath stops searching a directory. Skills active by name that only that
// path held show as missing. The directory is never touched.
func (s *Store) RemovePath(p string) error {
	cfg, err := s.config()
	if err != nil {
		return err
	}
	i := s.indexOfPath(cfg.Paths, p)
	if i < 0 {
		return fmt.Errorf("not a skill path: %s", p)
	}
	cfg.Paths = append(cfg.Paths[:i:i], cfg.Paths[i+1:]...)
	return config.SaveSkills(s.Dir, cfg)
}

// RenamePath replaces one search path with another, keeping its place.
func (s *Store) RenamePath(old, to string) error {
	if err := s.checkPath(to); err != nil {
		return err
	}
	cfg, err := s.config()
	if err != nil {
		return err
	}
	i := s.indexOfPath(cfg.Paths, old)
	if i < 0 {
		return fmt.Errorf("not a skill path: %s", old)
	}
	for j, q := range cfg.Paths {
		if j != i && s.expand(q) == s.expand(to) {
			return fmt.Errorf("already a skill path: %s", q)
		}
	}
	cfg.Paths = append([]string{}, cfg.Paths...)
	cfg.Paths[i] = to
	return config.SaveSkills(s.Dir, cfg)
}

func (s *Store) indexOfPath(paths []string, p string) int {
	for i, q := range paths {
		if q == p || s.expand(q) == s.expand(p) {
			return i
		}
	}
	return -1
}

// Filter returns the skills whose name or path fuzzy-matches query. Skills
// whose name matches come first, best match first; skills that match only by
// path follow. A path is long, so a short query would otherwise match scattered
// letters in it and outrank the skill named for the query. An empty query
// returns the list unchanged.
func Filter(list []Skill, query string) []Skill {
	query = strings.TrimSpace(query)
	if query == "" {
		return list
	}
	byName := make([]string, len(list))
	byPath := make([]string, len(list))
	for i, k := range list {
		byName[i], byPath[i] = k.Name, k.Path
	}
	var out []Skill
	named := map[int]bool{}
	for _, m := range fuzzy.Find(query, byName) {
		named[m.Index] = true
		out = append(out, list[m.Index])
	}
	for _, m := range fuzzy.Find(query, byPath) {
		if !named[m.Index] {
			out = append(out, list[m.Index])
		}
	}
	return out
}
