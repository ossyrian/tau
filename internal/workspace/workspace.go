// Package workspace adds, lists and removes the projects in a profile's
// workspace. A workspace entry is one of two kinds:
//
//	local  a copy of a directory, made inside the profile's workspace. The agent
//	       edits the copy; the original is never changed.
//	live   the user's real directory, bind-mounted into the workspace by
//	       `docker run`. The agent and the user edit the same files. It is
//	       recorded in config.yaml under workspaces.live.
//
// A local copy shows up in the running container at once, because the whole
// workspace directory is already mounted. A live entry needs the container to
// be recreated, because Docker cannot add a mount to a running container.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/container"
)

// Kind says how an entry is stored.
type Kind string

const (
	Local Kind = "local"
	Live  Kind = "live"
)

// Action says what Add did.
type Action string

const (
	Added     Action = "added"
	Refreshed Action = "refreshed"
	Unchanged Action = "unchanged"
)

// Entry is one project in the workspace.
type Entry struct {
	Name string
	Kind Kind
	// Source is the directory the entry came from. For a live entry it is the
	// config value ("~/dev/app" stays as written). A local copy does not record one.
	Source string
	// Host is the directory on the host that holds the files.
	Host string
	// Container is where the agent sees the files.
	Container string
}

// Result is the outcome of Add.
type Result struct {
	Action Action
	Entry  Entry
}

// Store manages one profile's workspace.
type Store struct {
	// Dir is the profile's directory, which holds config.yaml.
	Dir string
	// Root is the host directory mounted as the profile's workspace.
	Root string
	// Home is the user's home on the host, for "~/" in live sources.
	Home string
	// Mount is where Root appears in the container.
	Mount string
	// Clone copies src to dst, which does not exist yet. Merge copies src over
	// an existing dst and keeps files only dst has.
	Clone, Merge func(src, dst string) error
}

// Add puts the directory src in the workspace. With live, the real directory
// is mounted; otherwise it is copied. An empty name means the directory's own
// name. Adding the same directory again refreshes a copy and leaves a live
// entry as it is.
func (s *Store) Add(src, name string, live bool) (Result, error) {
	abs, err := resolveDir(src)
	if err != nil {
		return Result{}, err
	}
	if name == "" {
		name = filepath.Base(abs)
	}
	if err := config.CheckWorkspaceName(name); err != nil {
		return Result{}, err
	}
	if err := s.checkOverlap(abs); err != nil {
		return Result{}, err
	}
	w, err := config.LoadWorkspaces(s.Dir)
	if err != nil {
		return Result{}, err
	}
	if live {
		return s.addLive(w, abs, name)
	}
	return s.addLocal(w, abs, name)
}

// resolveDir returns src as an absolute path with symlinks resolved, and
// checks that it is a directory.
func resolveDir(src string) (string, error) {
	abs, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("no such directory: %s", src)
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return "", fmt.Errorf("not a directory: %s", src)
	}
	return real, nil
}

// checkOverlap refuses a source that is the workspace, holds it, or lies in it.
// A copy of a directory into itself never ends, and a mount of the workspace
// over itself would hide it.
func (s *Store) checkOverlap(src string) error {
	root := s.Root
	if real, err := filepath.EvalSymlinks(s.Root); err == nil {
		root = real
	}
	if within(root, src) || within(src, root) {
		return fmt.Errorf("%s overlaps the workspace %s: choose a directory outside it", src, root)
	}
	return nil
}

// within reports whether child is parent or lies under it.
func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Store) entry(name string, kind Kind, source, host string) Entry {
	return Entry{Name: name, Kind: kind, Source: source, Host: host, Container: strings.TrimRight(s.Mount, "/") + "/" + name}
}

func (s *Store) addLive(w config.Workspaces, abs, name string) (Result, error) {
	if have, ok := w.Live[name]; ok {
		if container.ExpandHome(have, s.Home) == abs {
			return Result{Unchanged, s.entry(name, Live, have, abs)}, nil
		}
		return Result{}, fmt.Errorf("workspace %q is already live from %s: remove it first, or choose another name", name, have)
	}
	mount := filepath.Join(s.Root, name)
	if st, err := os.Lstat(mount); err == nil {
		if !st.IsDir() {
			return Result{}, fmt.Errorf("workspace %q: %s is not a directory", name, mount)
		}
		if empty, err := isEmpty(mount); err != nil || !empty {
			return Result{}, fmt.Errorf("workspace %q already exists as a copy: remove it first, because a live mount would hide its files", name)
		}
	}
	if err := config.AddLive(s.Dir, name, abs); err != nil {
		return Result{}, err
	}
	return Result{Added, s.entry(name, Live, abs, abs)}, nil
}

func (s *Store) addLocal(w config.Workspaces, abs, name string) (Result, error) {
	if have, ok := w.Live[name]; ok {
		return Result{}, fmt.Errorf("workspace %q is already live from %s: remove it first, or choose another name", name, have)
	}
	dest := filepath.Join(s.Root, name)
	host := s.entry(name, Local, abs, dest)
	switch st, err := os.Lstat(dest); {
	case err == nil && !st.IsDir():
		return Result{}, fmt.Errorf("workspace %q: %s exists and is not a directory", name, dest)
	case err == nil:
		if err := s.Merge(abs, dest); err != nil {
			return Result{}, fmt.Errorf("refresh %s: %w", name, err)
		}
		return Result{Refreshed, host}, nil
	case !errors.Is(err, os.ErrNotExist):
		return Result{}, err
	}
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return Result{}, err
	}
	if err := s.Clone(abs, dest); err != nil {
		os.RemoveAll(dest) // no half copy
		return Result{}, fmt.Errorf("copy %s: %w", abs, err)
	}
	return Result{Added, host}, nil
}

func isEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	return len(entries) == 0, err
}

// List returns every entry: live ones from the config, and the directories in
// the workspace that are not a live mount point. Names are in order.
func (s *Store) List() ([]Entry, error) {
	w, err := config.LoadWorkspaces(s.Dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, n := range w.LiveNames() {
		out = append(out, s.entry(n, Live, w.Live[n], container.ExpandHome(w.Live[n], s.Home)))
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() || strings.HasPrefix(n, ".") {
			continue
		}
		if _, live := w.Live[n]; live {
			continue
		}
		out = append(out, s.entry(n, Local, "", filepath.Join(s.Root, n)))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Remove deletes a local copy, or unregisters a live entry. A live source is
// never touched, and neither is a file under its mount point.
func (s *Store) Remove(name string) (Entry, error) {
	if err := config.CheckWorkspaceName(name); err != nil {
		return Entry{}, err
	}
	w, err := config.LoadWorkspaces(s.Dir)
	if err != nil {
		return Entry{}, err
	}
	if src, ok := w.Live[name]; ok {
		e := s.entry(name, Live, src, container.ExpandHome(src, s.Home))
		if _, err := config.RemoveLive(s.Dir, name); err != nil {
			return Entry{}, err
		}
		os.Remove(filepath.Join(s.Root, name)) // the empty mount point; fails if not empty
		return e, nil
	}
	dest := filepath.Join(s.Root, name)
	st, err := os.Lstat(dest)
	if err != nil {
		return Entry{}, fmt.Errorf("no workspace %q", name)
	}
	if !st.IsDir() && st.Mode()&os.ModeSymlink == 0 {
		return Entry{}, fmt.Errorf("%q is not a workspace", name)
	}
	if err := os.RemoveAll(dest); err != nil { // a symlink is removed, not followed
		return Entry{}, err
	}
	return s.entry(name, Local, "", dest), nil
}

// Path returns the host directory of an entry, or the workspace itself for "".
func (s *Store) Path(name string) (string, error) {
	if name == "" {
		return s.Root, nil
	}
	if err := config.CheckWorkspaceName(name); err != nil {
		return "", err
	}
	w, err := config.LoadWorkspaces(s.Dir)
	if err != nil {
		return "", err
	}
	if src, ok := w.Live[name]; ok {
		return container.ExpandHome(src, s.Home), nil
	}
	dest := filepath.Join(s.Root, name)
	if st, err := os.Stat(dest); err != nil || !st.IsDir() {
		return "", fmt.Errorf("no workspace %q", name)
	}
	return dest, nil
}

// CloneDir copies src to dst. It uses a filesystem clone where there is one
// (macOS APFS), which is instant and takes almost no space, and a plain copy
// otherwise.
func CloneDir(src, dst string) error {
	if err := exec.Command("cp", "-cR", src, dst).Run(); err == nil {
		return nil
	}
	os.RemoveAll(dst)
	if out, err := exec.Command("cp", "-aR", src, dst).CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// MergeDir copies src over dst with rsync, keeping files only dst has.
func MergeDir(src, dst string) error {
	if out, err := exec.Command("rsync", "-a", src+"/", dst+"/").CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
