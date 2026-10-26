package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Workspace modes.
const (
	// WorkspaceIsolated gives a profile its own /workspace.
	WorkspaceIsolated = "isolated"
	// WorkspaceShared mounts the default profile's workspace, so profiles
	// that choose it see the same projects.
	WorkspaceShared = "shared"
)

// Workspaces is the "workspaces" section of config.yaml.
//
//	workspaces:
//	  mode: shared             # or isolated (the default)
//	  live:                    # real directories bind-mounted into the workspace
//	    myproject: ~/dev/myproject
//
// The default profile owns the shared workspace, so its own mode changes nothing.
// A live entry mounts the host directory at <workspace>/<name>: the agent and
// the user then edit the same files.
type Workspaces struct {
	Mode string
	// Live maps a name to a host directory, written as an absolute path or as
	// "~/..." (expanded when the container starts).
	Live map[string]string
}

// LiveNames lists the live workspaces in name order.
func (w Workspaces) LiveNames() []string { return sortedNames(w.Live) }

// liveNamePattern is one safe path component: it is joined to the workspace
// path, so it must not climb out, start with a dot or hold a separator.
var liveNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// CheckWorkspaceName fails unless name is one safe path component.
func CheckWorkspaceName(name string) error {
	if !liveNamePattern.MatchString(name) {
		return fmt.Errorf("workspace name %q must be letters, digits, '.', '_', '-', starting with a letter or digit", name)
	}
	return nil
}

func checkLive(name, src string) error {
	if err := CheckWorkspaceName(name); err != nil {
		return err
	}
	if src == "" {
		return fmt.Errorf("workspace %q has no source directory", name)
	}
	if !filepath.IsAbs(src) && src != "~" && !strings.HasPrefix(src, "~/") {
		return fmt.Errorf("workspace %q: source %q must be an absolute path or start with ~/", name, src)
	}
	return nil
}

type workspacesDoc struct {
	Workspaces *struct {
		Mode string            `yaml:"mode"`
		Live map[string]string `yaml:"live"`
	} `yaml:"workspaces"`
}

// LoadWorkspaces reads the workspaces section of config.yaml in dir. A
// missing file, section or mode gives WorkspaceIsolated.
func LoadWorkspaces(dir string) (Workspaces, error) {
	path := filepath.Join(dir, BaseFile)
	data, _, _, err := readOptional(path)
	if err != nil {
		return Workspaces{}, err
	}
	return parseWorkspaces(path, data)
}

func parseWorkspaces(source string, data []byte) (Workspaces, error) {
	var doc workspacesDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Workspaces{}, fmt.Errorf("%s: %w", source, err)
	}
	out := Workspaces{Mode: WorkspaceIsolated, Live: map[string]string{}}
	if doc.Workspaces == nil {
		return out, nil
	}
	if err := rejectUnknownKeys(data, "workspaces", "mode", "live"); err != nil {
		return Workspaces{}, fmt.Errorf("%s: %w", source, err)
	}
	for name, src := range doc.Workspaces.Live {
		if err := checkLive(name, src); err != nil {
			return Workspaces{}, fmt.Errorf("%s: workspaces.live: %w", source, err)
		}
	}
	if len(doc.Workspaces.Live) > 0 {
		out.Live = doc.Workspaces.Live
	}
	switch m := doc.Workspaces.Mode; m {
	case "":
	case WorkspaceIsolated, WorkspaceShared:
		out.Mode = m
	default:
		return Workspaces{}, fmt.Errorf("%s: workspaces.mode %q must be %q or %q", source, m, WorkspaceShared, WorkspaceIsolated)
	}
	return out, nil
}

// SaveWorkspaces writes the workspaces section of config.yaml in dir.
// Everything else in the file, including comments, is kept.
func SaveWorkspaces(dir string, w Workspaces) error {
	path := filepath.Join(dir, BaseFile)
	if w.Mode != WorkspaceIsolated && w.Mode != WorkspaceShared {
		return fmt.Errorf("workspaces.mode %q must be %q or %q", w.Mode, WorkspaceShared, WorkspaceIsolated)
	}
	data, mode, _, err := readOptional(path)
	if err != nil {
		return err
	}
	out, err := editRoot(data, func(root *yaml.Node) error {
		sec := ensureMapping(root, "workspaces")
		if sec == nil {
			return errors.New("workspaces must be a mapping")
		}
		setScalar(sec, "mode", w.Mode)
		return nil
	})
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return writeAtomic(path, out, mode)
}

// AddLive records a live workspace in the config.yaml in dir. An existing entry
// of the same name is replaced. The rest of the file, comments included, is kept.
func AddLive(dir, name, src string) error {
	if err := checkLive(name, src); err != nil {
		return err
	}
	return editWorkspaces(dir, func(sec *yaml.Node) error {
		live := ensureMapping(sec, "live")
		if live == nil {
			return errors.New("workspaces.live must be a mapping")
		}
		setScalar(live, name, src)
		return nil
	})
}

// RemoveLive deletes a live workspace from the config.yaml in dir and reports
// whether it was there.
func RemoveLive(dir, name string) (bool, error) {
	removed := false
	err := editWorkspaces(dir, func(sec *yaml.Node) error {
		live := findKey(sec, "live")
		if live == nil || live.Kind != yaml.MappingNode {
			return nil
		}
		if i := keyIndex(live, name); i >= 0 {
			live.Content = append(live.Content[:i], live.Content[i+2:]...)
			removed = true
		}
		if len(live.Content) == 0 {
			live.Style = yaml.FlowStyle
		}
		return nil
	})
	return removed, err
}

// editWorkspaces applies fn to the workspaces mapping and writes the file back.
func editWorkspaces(dir string, fn func(sec *yaml.Node) error) error {
	path := filepath.Join(dir, BaseFile)
	data, mode, _, err := readOptional(path)
	if err != nil {
		return err
	}
	out, err := editRoot(data, func(root *yaml.Node) error {
		sec := ensureMapping(root, "workspaces")
		if sec == nil {
			return errors.New("workspaces must be a mapping")
		}
		return fn(sec)
	})
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return writeAtomic(path, out, mode)
}
