// Package config loads and resolves the "packages" section of config.yaml.
//
// A package is a named install recipe: a script (or apt sugar), who runs it,
// what it needs first, and how to tell it is already installed. Files are
// parsed into Layers, layers merge in order, and Resolve joins the merged
// layer with the shipped catalog into an ordered, validated install plan.
//
// Only the top-level "packages" key is read. Other top-level keys belong to
// other parts of tau and are ignored here.
package config

import (
	"fmt"
	"regexp"
)

// Valid values for Package.As.
const (
	AsRoot = "root"
	AsUser = "user"
)

var (
	// Names become file names (/etc/tau/installed/<name>), so no slashes or dots up front.
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	// Env and vars keys become shell variable names.
	keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// Apt names are spliced into a command line.
	aptPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.+:_-]*$`)
)

// Package is one install recipe. In a Layer, a zero field means "not set by
// this file"; Merge and Resolve rely on that.
type Package struct {
	// Script holds shell scripts run in order. Mutually exclusive with Apt.
	Script []string
	// Apt holds apt package names. Sugar for an apt-get install script.
	Apt []string
	// As is AsRoot or AsUser. Empty means unset; Resolve defaults it to AsRoot.
	As string
	// Needs names packages that must be installed first.
	Needs []string
	// Check is a command; exit 0 means the package is already installed.
	Check string
	// Env is baked into the image as ENV.
	Env map[string]string
	// Vars are exported to the script as environment variables.
	Vars map[string]string
}

// Layer is one parsed config file.
type Layer struct {
	// Names lists the packages in declaration order.
	Names    []string
	Packages map[string]Package
}

func newLayer() *Layer {
	return &Layer{Packages: map[string]Package{}}
}

// Entry is a resolved package, ready to install.
type Entry struct {
	Name string
	Package
}

func (p Package) clone() Package {
	p.Script = cloneSlice(p.Script)
	p.Apt = cloneSlice(p.Apt)
	p.Needs = cloneSlice(p.Needs)
	p.Env = cloneMap(p.Env)
	p.Vars = cloneMap(p.Vars)
	return p
}

func cloneSlice(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string{}, s...)
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// overlay returns base with over applied on top.
//
// Script and Apt are one unit: if over sets either, both are replaced.
// Needs replaces as a whole. Env and Vars merge per key. As and Check
// override when non-empty.
func overlay(base, over Package) Package {
	out := base.clone()
	if over.Script != nil || over.Apt != nil {
		out.Script = cloneSlice(over.Script)
		out.Apt = cloneSlice(over.Apt)
	}
	if over.As != "" {
		out.As = over.As
	}
	if over.Needs != nil {
		out.Needs = cloneSlice(over.Needs)
	}
	if over.Check != "" {
		out.Check = over.Check
	}
	out.Env = mergeMaps(out.Env, over.Env)
	out.Vars = mergeMaps(out.Vars, over.Vars)
	return out
}

func mergeMaps(base, over map[string]string) map[string]string {
	if base == nil && over == nil {
		return nil
	}
	out := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

// validate checks a fully resolved package.
func (p Package) validate() error {
	hasScript, hasApt := len(p.Script) > 0, len(p.Apt) > 0
	switch {
	case hasScript && hasApt:
		return fmt.Errorf("set script or apt, not both")
	case !hasScript && !hasApt:
		return fmt.Errorf("has no script or apt, and is not in the catalog")
	}
	for _, s := range p.Script {
		if s == "" {
			return fmt.Errorf("script has an empty entry")
		}
	}
	for _, a := range p.Apt {
		if !aptPattern.MatchString(a) {
			return fmt.Errorf("invalid apt package name %q", a)
		}
	}
	if p.As != AsRoot && p.As != AsUser {
		return fmt.Errorf("as must be %q or %q, got %q", AsRoot, AsUser, p.As)
	}
	if hasApt && p.As == AsUser {
		return fmt.Errorf("apt installs need as: root")
	}
	for _, n := range p.Needs {
		if !namePattern.MatchString(n) {
			return fmt.Errorf("invalid package name %q in needs", n)
		}
	}
	for k := range p.Env {
		if !keyPattern.MatchString(k) {
			return fmt.Errorf("invalid env name %q", k)
		}
	}
	for k := range p.Vars {
		if !keyPattern.MatchString(k) {
			return fmt.Errorf("invalid vars name %q", k)
		}
	}
	return nil
}

// Resolve turns the user's layer into an ordered install plan.
//
// Only packages named in user are installed; catalog is a library of defaults
// and extra definitions, not a list of things to install. A user entry is laid
// over the catalog entry of the same name. A package in needs but not in user
// is pulled in from catalog. Order is declaration order, adjusted so that each
// package comes after everything it needs.
func Resolve(user, catalog *Layer) ([]Entry, error) {
	if catalog == nil {
		catalog = newLayer()
	}
	defs := map[string]Package{}
	var declared []string

	add := func(name string, p Package) {
		defs[name] = p
		declared = append(declared, name)
	}
	for _, name := range user.Names {
		base := catalog.Packages[name] // zero Package if absent
		add(name, overlay(base, user.Packages[name]))
	}
	// Pull in needs that the user did not list, breadth-first so the result is deterministic.
	for i := 0; i < len(declared); i++ {
		name := declared[i]
		for _, need := range defs[name].Needs {
			if _, ok := defs[need]; ok {
				continue
			}
			if !namePattern.MatchString(need) {
				return nil, fmt.Errorf("package %q: invalid package name %q in needs", name, need)
			}
			p, ok := catalog.Packages[need]
			if !ok {
				return nil, fmt.Errorf("package %q needs %q, which is not defined in config or catalog", name, need)
			}
			add(need, p.clone())
		}
	}

	for _, name := range declared {
		p := defs[name]
		if p.As == "" {
			p.As = AsRoot
		}
		defs[name] = p
		if err := p.validate(); err != nil {
			return nil, fmt.Errorf("package %q: %w", name, err)
		}
	}
	return sortByNeeds(declared, defs)
}

// sortByNeeds orders names depth-first in declaration order, dependencies first.
func sortByNeeds(declared []string, defs map[string]Package) ([]Entry, error) {
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var out []Entry
	var stack []string

	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("dependency cycle: %s", cyclePath(stack, name))
		}
		state[name] = visiting
		stack = append(stack, name)
		for _, need := range defs[name].Needs {
			if err := visit(need); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = done
		out = append(out, Entry{Name: name, Package: defs[name]})
		return nil
	}
	for _, name := range declared {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// cyclePath renders "a -> b -> a" from the visit stack and the repeated name.
func cyclePath(stack []string, repeat string) string {
	start := 0
	for i, n := range stack {
		if n == repeat {
			start = i
			break
		}
	}
	path := ""
	for _, n := range stack[start:] {
		path += n + " -> "
	}
	return path + repeat
}

// Validate checks a resolved entry: a safe name and a valid definition.
// Resolve already does this; it is exported for code that renders entries it
// did not resolve itself.
func (e Entry) Validate() error {
	if !namePattern.MatchString(e.Name) {
		return fmt.Errorf("invalid package name %q", e.Name)
	}
	return e.Package.validate()
}
