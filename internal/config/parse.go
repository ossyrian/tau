package config

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var packageFields = map[string]bool{
	"script": true, "apt": true, "as": true, "needs": true,
	"check": true, "env": true, "vars": true,
}

// Parse reads one config file into a Layer. source names the file in errors.
// An empty file, or one with no "packages" key, gives an empty Layer.
func Parse(source string, data []byte) (*Layer, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	layer := newLayer()
	if doc.Kind == 0 {
		return layer, nil
	}
	p := parser{source: source}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, p.errf(root, "top level must be a mapping")
	}
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value != "packages" {
			continue
		}
		if err := p.packages(layer, root.Content[i+1]); err != nil {
			return nil, err
		}
	}
	return layer, nil
}

type parser struct{ source string }

func (p parser) errf(n *yaml.Node, format string, args ...any) error {
	return fmt.Errorf("%s:%d: %s", p.source, n.Line, fmt.Sprintf(format, args...))
}

func (p parser) packages(layer *Layer, n *yaml.Node) error {
	if isNull(n) {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return p.errf(n, "packages must be a mapping of name to package")
	}
	for i := 0; i < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		name := key.Value
		if !namePattern.MatchString(name) {
			return p.errf(key, "invalid package name %q (use lowercase letters, digits, '.', '_', '-')", name)
		}
		if _, dup := layer.Packages[name]; dup {
			return p.errf(key, "package %q is defined twice", name)
		}
		pkg, err := p.pkg(name, val)
		if err != nil {
			return err
		}
		layer.Names = append(layer.Names, name)
		layer.Packages[name] = pkg
	}
	return nil
}

func (p parser) pkg(name string, n *yaml.Node) (Package, error) {
	var pkg Package
	if isNull(n) {
		return pkg, nil
	}
	if n.Kind != yaml.MappingNode {
		return pkg, p.errf(n, "package %q must be a mapping", name)
	}
	for i := 0; i < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		if !packageFields[key.Value] {
			return pkg, p.errf(key, "package %q: unknown field %q (valid: %s)", name, key.Value, fieldList())
		}
		if err := p.field(&pkg, key.Value, val); err != nil {
			return pkg, fmt.Errorf("package %q: %w", name, err)
		}
	}
	if pkg.Script != nil && pkg.Apt != nil {
		return pkg, p.errf(n, "package %q: set script or apt, not both", name)
	}
	return pkg, nil
}

func (p parser) field(pkg *Package, field string, n *yaml.Node) error {
	var err error
	switch field {
	case "script":
		pkg.Script, err = p.stringList(field, n)
	case "apt":
		pkg.Apt, err = p.stringList(field, n)
	case "needs":
		pkg.Needs, err = p.stringList(field, n)
	case "as":
		pkg.As, err = p.str(field, n)
	case "check":
		pkg.Check, err = p.str(field, n)
	case "env":
		pkg.Env, err = p.strMap(field, n)
	case "vars":
		pkg.Vars, err = p.strMap(field, n)
	}
	return err
}

func (p parser) str(field string, n *yaml.Node) (string, error) {
	if n.Kind != yaml.ScalarNode || isNull(n) {
		return "", p.errf(n, "%s must be a string", field)
	}
	return n.Value, nil
}

// stringList accepts one string or a list of strings. The result is never nil.
func (p parser) stringList(field string, n *yaml.Node) ([]string, error) {
	out := []string{}
	switch n.Kind {
	case yaml.ScalarNode:
		s, err := p.str(field, n)
		if err != nil {
			return nil, err
		}
		return append(out, s), nil
	case yaml.SequenceNode:
		for _, item := range n.Content {
			s, err := p.str(field, item)
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, p.errf(n, "%s must be a string or a list of strings", field)
}

// strMap reads a mapping of scalars. Numbers and booleans keep their source text,
// so "version: 2.10" stays "2.10".
func (p parser) strMap(field string, n *yaml.Node) (map[string]string, error) {
	if n.Kind != yaml.MappingNode {
		return nil, p.errf(n, "%s must be a mapping", field)
	}
	out := make(map[string]string, len(n.Content)/2)
	for i := 0; i < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		if !keyPattern.MatchString(key.Value) {
			return nil, p.errf(key, "invalid %s name %q", field, key.Value)
		}
		if val.Kind != yaml.ScalarNode || isNull(val) {
			return nil, p.errf(val, "%s.%s must be a string, number, or boolean", field, key.Value)
		}
		if _, dup := out[key.Value]; dup {
			return nil, p.errf(key, "%s.%s is defined twice", field, key.Value)
		}
		out[key.Value] = val.Value
	}
	return out, nil
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

func fieldList() string {
	names := make([]string, 0, len(packageFields))
	for f := range packageFields {
		names = append(names, f)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
