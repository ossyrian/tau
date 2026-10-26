package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"slices"

	"gopkg.in/yaml.v3"
)

// Action resolves one item of drift between config.yaml and config.runtime.yaml.
type Action string

const (
	// Promote copies a runtime-only package into config.yaml.
	Promote Action = "promote"
	// Discard removes a runtime-only package from the runtime file.
	Discard Action = "discard"
	// Adopt replaces a package's definition in config.yaml with the runtime one.
	Adopt Action = "adopt"
	// Revert replaces a package's definition in the runtime file with the one in config.yaml.
	Revert Action = "revert"
)

// ErrNotApplicable means the action does not fit the package's current drift
// (or there is no drift to resolve). Nothing was changed.
var ErrNotApplicable = errors.New("action not applicable")

// Apply resolves the drift on one package. It re-reads both files from disk,
// checks that the action fits what is there, and changes exactly one file:
//
//	Promote, Adopt  edit config.yaml (comments and order are kept)
//	Discard, Revert edit config.runtime.yaml
//
// Edits to config.yaml are validated first: the result must still resolve.
// Removing a runtime package that another runtime package needs is refused.
func (c *Config) Apply(name string, action Action) error {
	basePath := filepath.Join(c.Dir, BaseFile)
	runtimePath := filepath.Join(c.Dir, RuntimeFile)

	baseData, baseMode, _, err := readOptional(basePath)
	if err != nil {
		return err
	}
	base, err := Parse(basePath, baseData)
	if err != nil {
		return err
	}
	rtData, rtMode, exists, err := readOptional(runtimePath)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s does not exist", ErrNotApplicable, runtimePath)
	}
	rt, err := Parse(runtimePath, rtData)
	if err != nil {
		return err
	}
	d := Compare(base, rt)

	switch action {
	case Promote:
		if !slices.Contains(d.RuntimeOnly, name) {
			return fmt.Errorf("%w: %q is not a runtime-only package", ErrNotApplicable, name)
		}
		out, err := appendPackage(baseData, name, rt.Packages[name])
		if err != nil {
			return fmt.Errorf("%s: %w", basePath, err)
		}
		return c.commit(basePath, out, baseMode, name, rt.Packages[name], func(l *Layer) error {
			c.Base = l
			return nil
		})
	case Adopt:
		if !slices.Contains(d.Changed, name) {
			return fmt.Errorf("%w: %q has no changed definition", ErrNotApplicable, name)
		}
		out, err := replacePackage(baseData, name, rt.Packages[name])
		if err != nil {
			return fmt.Errorf("%s: %w", basePath, err)
		}
		return c.commit(basePath, out, baseMode, name, rt.Packages[name], func(l *Layer) error {
			c.Base = l
			return nil
		})
	case Discard:
		if !slices.Contains(d.RuntimeOnly, name) {
			return fmt.Errorf("%w: %q is not a runtime-only package", ErrNotApplicable, name)
		}
		for _, other := range rt.Names {
			if other != name && slices.Contains(rt.Packages[other].Needs, name) {
				return fmt.Errorf("cannot discard %q: %q needs it", name, other)
			}
		}
		out, err := removePackage(rtData, name)
		if err != nil {
			return fmt.Errorf("%s: %w", runtimePath, err)
		}
		return c.commitRuntime(runtimePath, out, rtMode)
	case Revert:
		if !slices.Contains(d.Changed, name) {
			return fmt.Errorf("%w: %q has no changed definition", ErrNotApplicable, name)
		}
		out, err := replacePackage(rtData, name, base.Packages[name])
		if err != nil {
			return fmt.Errorf("%s: %w", runtimePath, err)
		}
		return c.commitRuntime(runtimePath, out, rtMode)
	}
	return fmt.Errorf("%w: unknown action %q", ErrNotApplicable, action)
}

// commit validates and writes a new config.yaml, then checks the package read back.
func (c *Config) commit(path string, out []byte, mode os.FileMode, name string, want Package, set func(*Layer) error) error {
	written, err := Parse(path, out)
	if err != nil {
		return fmt.Errorf("refusing to write %s: result does not parse: %w", path, err)
	}
	if !samePackage(written.Packages[name], want) {
		return fmt.Errorf("refusing to write %s: %q would not read back unchanged", path, name)
	}
	if _, err := Resolve(written, c.Catalog); err != nil {
		return err
	}
	if err := writeAtomic(path, out, mode); err != nil {
		return err
	}
	return set(written)
}

// commitRuntime writes a new config.runtime.yaml. Its entries are not validated
// as a build plan, because the runtime file never drives a build.
func (c *Config) commitRuntime(path string, out []byte, mode os.FileMode) error {
	written, err := Parse(path, out)
	if err != nil {
		return fmt.Errorf("refusing to write %s: result does not parse: %w", path, err)
	}
	if err := writeAtomic(path, out, mode); err != nil {
		return err
	}
	c.Runtime = written
	return nil
}

// Settle deletes config.runtime.yaml when it holds no drift, so the next
// runtime edit starts from a fresh copy of config.yaml. It reads both files
// from disk. It reports whether the file was deleted. Packages that exist only
// in config.yaml do not count as drift.
func (c *Config) Settle() (bool, error) {
	basePath := filepath.Join(c.Dir, BaseFile)
	runtimePath := filepath.Join(c.Dir, RuntimeFile)
	baseData, _, _, err := readOptional(basePath)
	if err != nil {
		return false, err
	}
	base, err := Parse(basePath, baseData)
	if err != nil {
		return false, err
	}
	rtData, _, exists, err := readOptional(runtimePath)
	if err != nil || !exists {
		return false, err
	}
	rt, err := Parse(runtimePath, rtData)
	if err != nil {
		return false, err
	}
	if Compare(base, rt).NeedsResolution() {
		return false, nil
	}
	if err := os.Remove(runtimePath); err != nil {
		return false, err
	}
	c.Base, c.Runtime = base, nil
	return true, nil
}

// YAML renders the package as the YAML block that would appear under its name,
// for showing to the user.
func (p Package) YAML() string {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(packageNode(p))
	_ = enc.Close()
	if s := buf.String(); s != "{}\n" {
		return s
	}
	return ""
}
