package config

import "reflect"

// Diff is the drift between config.yaml (the source of truth) and
// config.runtime.yaml (what accumulated at runtime). It compares parsed
// packages, so comments, key order and equivalent spellings do not count.
type Diff struct {
	// RuntimeOnly lists packages in the runtime file that config.yaml lacks,
	// in runtime-file order. These are drift the user must resolve.
	RuntimeOnly []string
	// Changed lists packages in both files whose definitions differ, in
	// runtime-file order. These are drift the user must resolve.
	Changed []string
	// BaseOnly lists packages in config.yaml that the runtime file lacks, in
	// config.yaml order. These are intended, not drift: the next build adds them.
	BaseOnly []string
}

// NeedsResolution reports whether the runtime file holds drift the user has to
// decide about. BaseOnly alone never does.
func (d Diff) NeedsResolution() bool {
	return len(d.RuntimeOnly) > 0 || len(d.Changed) > 0
}

// Compare diffs the runtime layer against the baseline. A nil runtime layer
// means no runtime file exists, so there is nothing to compare and the result is empty.
func Compare(base, runtime *Layer) Diff {
	var d Diff
	if runtime == nil {
		return d
	}
	for _, name := range runtime.Names {
		b, ok := base.Packages[name]
		switch {
		case !ok:
			d.RuntimeOnly = append(d.RuntimeOnly, name)
		case !equivalent(b, runtime.Packages[name]):
			d.Changed = append(d.Changed, name)
		}
	}
	for _, name := range base.Names {
		if _, ok := runtime.Packages[name]; !ok {
			d.BaseOnly = append(d.BaseOnly, name)
		}
	}
	return d
}

// Diff compares the runtime file against config.yaml.
func (c *Config) Diff() Diff {
	return Compare(c.Base, c.Runtime)
}

// equivalent reports whether two packages mean the same thing: an unset "as"
// equals "root", and nil equals empty for lists and maps.
func equivalent(a, b Package) bool {
	return reflect.DeepEqual(normalize(a), normalize(b))
}

func normalize(p Package) Package {
	if p.As == "" {
		p.As = AsRoot
	}
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
