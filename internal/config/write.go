package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"maps"
	"slices"

	"gopkg.in/yaml.v3"
)

// ErrExists means the package name is already defined in the config being edited.
var ErrExists = errors.New("package already defined")

// AddRuntime records a new package in config.runtime.yaml and updates c to match.
//
// config.runtime.yaml is a copy of config.yaml plus the drift from runtime
// operations. It does not change what a build installs; the user resolves the
// drift into config.yaml.
// If it does not exist yet, AddRuntime first seeds it from config.yaml (or from
// nothing, if that is missing too), so existing entries and other settings
// carry over, and then applies the edit. Later changes to config.yaml are not
// copied in; Diff reports them. The file is re-encoded on every write:
// comments, key order and other top-level keys are kept, but whitespace and
// quoting may be normalized. config.yaml itself is never written.
//
// It only adds names. A name already in config.yaml or in the runtime file
// gives ErrExists and changes nothing, so a caller can never alter an entry the
// user wrote or redefine one already recorded. An
// empty Package is allowed when the catalog defines the name: that records
// "install this catalog package".
//
// AddRuntime re-reads the files from disk, so an edit made after Load is
// neither lost nor overwritten. It checks that the config still resolves with
// the new entry, writes through a temp file and a rename, and then reads the
// result back and compares it to what was asked for. Comments, key order and
// other top-level keys survive.
func (c *Config) AddRuntime(name string, p Package) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid package name %q (use lowercase letters, digits, '.', '_', '-')", name)
	}
	basePath := filepath.Join(c.Dir, BaseFile)
	runtimePath := filepath.Join(c.Dir, RuntimeFile)

	baseData, _, _, err := readOptional(basePath)
	if err != nil {
		return err
	}
	base, err := Parse(basePath, baseData)
	if err != nil {
		return err
	}
	if _, ok := base.Packages[name]; ok {
		return fmt.Errorf("%w: %q is in %s", ErrExists, name, basePath)
	}

	data, mode, exists, err := readOptional(runtimePath)
	if err != nil {
		return err
	}
	if !exists { // first edit: start from a copy of config.yaml
		data, mode, _, err = readOptional(basePath)
		if err != nil {
			return err
		}
	}
	current, err := Parse(runtimePath, data)
	if err != nil {
		return err
	}
	if _, ok := current.Packages[name]; ok {
		return fmt.Errorf("%w: %q is in %s", ErrExists, name, runtimePath)
	}

	// A build installs config.yaml, so a new entry may depend on a package that
	// only config.yaml has. Check the runtime file as if those were present.
	p = p.clone()
	view := &Layer{Names: append([]string{}, current.Names...), Packages: map[string]Package{}}
	for n, pkg := range current.Packages {
		view.Packages[n] = pkg
	}
	view.Names = append(view.Names, name)
	view.Packages[name] = p
	for _, n := range base.Names {
		if _, ok := view.Packages[n]; !ok {
			view.Names = append(view.Names, n)
			view.Packages[n] = base.Packages[n]
		}
	}
	if _, err := Resolve(view, c.Catalog); err != nil {
		return err
	}

	out, err := appendPackage(data, name, p)
	if err != nil {
		return fmt.Errorf("%s: %w", runtimePath, err)
	}
	written, err := Parse(runtimePath, out)
	if err != nil {
		return fmt.Errorf("refusing to write %s: result does not parse: %w", runtimePath, err)
	}
	if !samePackage(written.Packages[name], p) {
		return fmt.Errorf("refusing to write %s: %q would not read back unchanged", runtimePath, name)
	}
	if err := writeAtomic(runtimePath, out, mode); err != nil {
		return err
	}
	c.Base, c.Runtime = base, written
	return nil
}

// samePackage compares two packages, treating nil and empty maps and lists alike.
func samePackage(a, b Package) bool {
	norm := func(p Package) Package {
		if len(p.Script) == 0 {
			p.Script = nil
		}
		if len(p.Apt) == 0 {
			p.Apt = nil
		}
		if len(p.Needs) == 0 {
			p.Needs = nil
		}
		if len(p.Env) == 0 {
			p.Env = nil
		}
		if len(p.Vars) == 0 {
			p.Vars = nil
		}
		return p
	}
	return reflect.DeepEqual(norm(a), norm(b))
}

// readOptional reads path. A missing file gives exists=false, nil data and mode 0644.
func readOptional(path string) (data []byte, mode os.FileMode, exists bool, err error) {
	data, err = os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0o644, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, false, err
	}
	return data, info.Mode().Perm(), true, nil
}

// editRoot parses data, applies fn to the top-level mapping, and encodes the
// result. Comments, key order and other top-level keys are kept. Empty or
// comment-only input keeps its leading comments.
func editRoot(data []byte, fn func(root *yaml.Node) error) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var prefix []byte
	if doc.Kind == 0 {
		// The decoder drops comments from an empty document, so keep the original text.
		prefix = prefixComments(data)
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("top level must be a mapping")
	}
	if err := fn(root); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.Write(prefix)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// editPackages applies fn to the "packages" mapping in data. A missing or null
// "packages" key becomes an empty mapping first.
func editPackages(data []byte, fn func(pkgs *yaml.Node) error) ([]byte, error) {
	return editRoot(data, func(root *yaml.Node) error {
		pkgs := findKey(root, "packages")
		switch {
		case pkgs == nil:
			pkgs = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			root.Content = append(root.Content, scalar("packages"), pkgs)
		case isNull(pkgs) || (pkgs.Kind == yaml.MappingNode && len(pkgs.Content) == 0):
			*pkgs = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		case pkgs.Kind != yaml.MappingNode:
			return errors.New("packages must be a mapping")
		}
		return fn(pkgs)
	})
}

// appendPackage adds name to the "packages" mapping in data.
func appendPackage(data []byte, name string, p Package) ([]byte, error) {
	return editPackages(data, func(pkgs *yaml.Node) error {
		pkgs.Content = append(pkgs.Content, scalar(name), packageNode(p))
		return nil
	})
}

// removePackage deletes name from the "packages" mapping in data.
func removePackage(data []byte, name string) ([]byte, error) {
	return editPackages(data, func(pkgs *yaml.Node) error {
		i := keyIndex(pkgs, name)
		if i < 0 {
			return fmt.Errorf("package %q not found", name)
		}
		pkgs.Content = append(pkgs.Content[:i], pkgs.Content[i+2:]...)
		return nil
	})
}

// replacePackage swaps the definition of name in place, keeping its position
// and the comments above its key.
func replacePackage(data []byte, name string, p Package) ([]byte, error) {
	return editPackages(data, func(pkgs *yaml.Node) error {
		i := keyIndex(pkgs, name)
		if i < 0 {
			return fmt.Errorf("package %q not found", name)
		}
		pkgs.Content[i+1] = packageNode(p)
		return nil
	})
}

func keyIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// prefixComments returns the leading comment and blank lines of data.
func prefixComments(data []byte) []byte {
	var keep []byte
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		t := bytes.TrimSpace(line)
		if len(t) > 0 && t[0] != '#' {
			break
		}
		keep = append(keep, line...)
	}
	if len(keep) > 0 && !bytes.HasSuffix(keep, []byte("\n")) {
		keep = append(keep, '\n')
	}
	return keep
}

func findKey(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func scalar(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// str builds a string node. yaml.v3 picks a literal block for multi-line text
// and quotes text that YAML would read as another type. The one case it gets
// wrong is text a block cannot hold unchanged (see needsDoubleQuotes), so that
// is forced into double quotes.
func str(s string) *yaml.Node {
	n := scalar(s)
	if needsDoubleQuotes(s) {
		n.Style = yaml.DoubleQuotedStyle
	}
	return n
}

func containsNewline(s string) bool {
	for _, r := range s {
		if r == '\n' {
			return true
		}
	}
	return false
}

// needsDoubleQuotes reports text that block scalars cannot hold unchanged:
// carriage returns and other control characters, a leading space or blank line
// (the block indent is guessed from the first line), or trailing spaces on a line.
func needsDoubleQuotes(s string) bool {
	for _, r := range s {
		if r != '\n' && r != '\t' && (r < 0x20 || r == 0x7f) {
			return true
		}
	}
	if !containsNewline(s) {
		return false
	}
	lines := bytes.Split([]byte(s), []byte("\n"))
	if len(lines[0]) == 0 || lines[0][0] == ' ' || lines[0][0] == '\t' {
		return true
	}
	for _, l := range lines {
		if len(l) > 0 && (l[len(l)-1] == ' ' || l[len(l)-1] == '\t') {
			return true
		}
	}
	return false
}

func strOrList(vals []string) *yaml.Node {
	if len(vals) == 1 {
		return str(vals[0])
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, v := range vals {
		seq.Content = append(seq.Content, str(v))
	}
	return seq
}

func strMapNode(m map[string]string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		n.Content = append(n.Content, scalar(k), str(m[k]))
	}
	return n
}

func packageNode(p Package) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	add := func(key string, val *yaml.Node) { n.Content = append(n.Content, scalar(key), val) }
	if len(p.Script) > 0 {
		add("script", strOrList(p.Script))
	}
	if len(p.Apt) > 0 {
		add("apt", strOrList(p.Apt))
	}
	if p.As != "" {
		add("as", str(p.As))
	}
	if len(p.Needs) > 0 {
		add("needs", strOrList(p.Needs))
	}
	if p.Check != "" {
		add("check", str(p.Check))
	}
	if len(p.Env) > 0 {
		add("env", strMapNode(p.Env))
	}
	if len(p.Vars) > 0 {
		add("vars", strMapNode(p.Vars))
	}
	if len(n.Content) == 0 {
		n.Style = yaml.FlowStyle // an empty body prints as {}
	}
	return n
}

// writeAtomic replaces path with data via a temp file in the same directory.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tau-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
