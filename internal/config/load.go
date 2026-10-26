package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// File names inside the config directory (normally ~/.tau).
const (
	BaseFile    = "config.yaml"         // hand-written source of truth
	RuntimeFile = "config.runtime.yaml" // copy of config.yaml plus drift from runtime operations
)

// Config is the loaded configuration: the shipped catalog, the baseline, and
// the runtime copy if it exists.
//
// config.yaml is the source of truth and the only file Resolve installs from.
// config.runtime.yaml accumulates drift from runtime operations. Compare them
// with Diff; the user decides what to do with the differences.
type Config struct {
	Dir     string
	Catalog *Layer
	Base    *Layer // config.yaml
	Runtime *Layer // config.runtime.yaml; nil when the file does not exist
}

// Load reads the config directory. Missing files count as empty. catalog may
// be nil. The catalog file system holds *.yaml files at its root.
func Load(dir string, catalog fs.FS) (*Config, error) {
	cat, err := LoadCatalog(catalog)
	if err != nil {
		return nil, err
	}
	c := &Config{Dir: dir, Catalog: cat}
	if c.Base, err = loadFile(filepath.Join(dir, BaseFile)); err != nil {
		return nil, err
	}
	runtimePath := filepath.Join(dir, RuntimeFile)
	if _, statErr := os.Stat(runtimePath); statErr == nil {
		if c.Runtime, err = loadFile(runtimePath); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func loadFile(path string) (*Layer, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return newLayer(), nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(path, data)
}

// LoadCatalog reads every *.yaml file at the root of fsys, in name order.
// A package name defined in two files is an error. A nil fsys gives an empty catalog.
func LoadCatalog(fsys fs.FS) (*Layer, error) {
	out := newLayer()
	if fsys == nil {
		return out, nil
	}
	dirEntries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	sort.Slice(dirEntries, func(i, j int) bool { return dirEntries[i].Name() < dirEntries[j].Name() })

	definedIn := map[string]string{}
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".yaml") {
			continue
		}
		data, err := fs.ReadFile(fsys, de.Name())
		if err != nil {
			return nil, err
		}
		l, err := Parse(de.Name(), data)
		if err != nil {
			return nil, err
		}
		for _, name := range l.Names {
			if prev, dup := definedIn[name]; dup {
				return nil, fmt.Errorf("catalog package %q is defined in both %s and %s", name, prev, de.Name())
			}
			definedIn[name] = de.Name()
			out.Names = append(out.Names, name)
			out.Packages[name] = l.Packages[name]
		}
	}
	return out, nil
}

// Resolve returns the ordered install plan for config.yaml, with the catalog
// underneath. The runtime file is not read: drift in it only takes effect after
// the user resolves it into config.yaml.
func (c *Config) Resolve() ([]Entry, error) {
	return Resolve(c.Base, c.Catalog)
}
