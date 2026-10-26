package config

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Values of Proxy.RunsIn.
const (
	// EmptyDirectory runs the proxy in an empty private directory. It sees none
	// of the agent's files. This is the default.
	EmptyDirectory = "empty_directory"
	// AgentDirectory runs the proxy in the directory the agent called it from,
	// which must be inside the profile's workspace. The proxy then runs
	// whatever the agent left in that directory (git hooks, for one), so use it
	// only for tools that must see the files.
	AgentDirectory = "agent_directory"
)

// ProxyPackage is a hidden install: software only a proxy can run. It is
// installed once, as root, into a directory the agent cannot read. The script
// gets that directory in $PREFIX.
type ProxyPackage struct {
	Script []string
	Check  string
	Vars   map[string]string
}

// Proxy is one credentialed command that runs outside the agent's reach.
//
// Three fields say where the software is, which file runs, and what the agent
// types. Exactly one of (Package with Binary) or SystemBinary is set.
type Proxy struct {
	// Package names an entry of Proxies.Packages, the hidden install holding
	// the software, and Binary is the file to run inside it, relative to the
	// install's prefix ("bin/aws").
	Package string
	Binary  string
	// SystemBinary is the absolute path of a program already on the system
	// ("/usr/bin/git"), for tools that need no hidden install.
	SystemBinary string
	// AgentCommand is the name the agent types. It is the only part of the
	// proxy the agent sees.
	AgentCommand string
	Description  string
	// Deny lists argument sequences the proxy refuses. "configure export-credentials"
	// refuses any call whose arguments hold those two words next to each other;
	// "--debug" refuses any call with that argument.
	Deny []string
	// RunsIn is EmptyDirectory or AgentDirectory.
	RunsIn string
	// EnvFrom is a command that runs on the host and prints KEY=VALUE lines,
	// for credentials that are minted rather than stored (SSO, assume-role).
	EnvFrom string
}

// InAgentDirectory reports whether the proxy runs in the agent's directory.
func (p Proxy) InAgentDirectory() bool { return p.RunsIn == AgentDirectory }

// Proxies is the "proxies" section of config.yaml.
//
//	proxies:
//	  packages:                   # only in the default profile's config
//	    aws-cli: {script: ..., check: ...}
//	  defs:                       # only in the default profile's config
//	    aws-prod:
//	      package: aws-cli
//	      binary: bin/aws
//	      agent_command: aws
//	      deny: [configure export-credentials]
//	      env_from: aws configure export-credentials --profile ro --format env
//	  active: [aws-prod]          # which proxies this profile may use
//
// Packages and definitions are shared by every profile. Each profile chooses
// which proxies it can call.
type Proxies struct {
	Packages map[string]ProxyPackage
	Defs     map[string]Proxy
	Active   []string
}

// Names lists the defined proxies in name order.
func (p Proxies) Names() []string { return sortedNames(p.Defs) }

// PackageNames lists the hidden packages in name order.
func (p Proxies) PackageNames() []string { return sortedNames(p.Packages) }

func sortedNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// stringOrList reads a YAML string or list of strings.
type stringOrList []string

func (s *stringOrList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*s = []string{n.Value}
		return nil
	case yaml.SequenceNode:
		var l []string
		if err := n.Decode(&l); err != nil {
			return err
		}
		*s = l
		return nil
	}
	return errors.New("must be a string or a list of strings")
}

type proxyDoc struct {
	Package      string   `yaml:"package"`
	Binary       string   `yaml:"binary"`
	SystemBinary string   `yaml:"system_binary"`
	AgentCommand string   `yaml:"agent_command"`
	Description  string   `yaml:"description"`
	Deny         []string `yaml:"deny"`
	EnvFrom      string   `yaml:"env_from"`
	RunsIn       string   `yaml:"runs_in"`
}

type proxyPackageDoc struct {
	Script stringOrList      `yaml:"script"`
	Check  string            `yaml:"check"`
	Vars   map[string]string `yaml:"vars"`
}

type proxiesDoc struct {
	Packages map[string]proxyPackageDoc `yaml:"packages"`
	Defs     map[string]proxyDoc        `yaml:"defs"`
	Active   []string                   `yaml:"active"`
}

// A proxy name becomes part of a container user name, which allows no dots.
var proxyNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)

var agentCommandPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// LoadProxies reads the proxies section of the default profile's config.yaml
// in dir, where proxies are defined.
func LoadProxies(dir string) (Proxies, error) { return LoadProxiesWith(dir, nil) }

// LoadProxiesWith reads the proxies section of config.yaml in dir. inherited
// holds the packages and definitions from the default profile; nil means dir
// is the default profile itself, which may define them. Any other profile only
// chooses from the inherited ones.
func LoadProxiesWith(dir string, inherited *Proxies) (Proxies, error) {
	path := filepath.Join(dir, BaseFile)
	data, _, _, err := readOptional(path)
	if err != nil {
		return Proxies{}, err
	}
	out, err := parseProxies(data, inherited)
	if err != nil {
		return Proxies{}, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

func parseProxies(data []byte, inherited *Proxies) (Proxies, error) {
	var top struct {
		Proxies yaml.Node `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(data, &top); err != nil {
		return Proxies{}, err
	}
	var doc proxiesDoc
	if top.Proxies.Kind != 0 && !isNull(&top.Proxies) {
		raw, err := yaml.Marshal(&top.Proxies)
		if err != nil {
			return Proxies{}, err
		}
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		dec.KnownFields(true) // a typo in a security setting must not be ignored
		if err := dec.Decode(&doc); err != nil {
			return Proxies{}, fmt.Errorf("proxies: %w", err)
		}
	}

	if inherited != nil && (len(doc.Defs) > 0 || len(doc.Packages) > 0) {
		return Proxies{}, errors.New("proxies.defs and proxies.packages are only read from the default profile's config.yaml")
	}
	out := Proxies{Packages: map[string]ProxyPackage{}, Defs: map[string]Proxy{}, Active: doc.Active}
	if inherited != nil {
		for n, pk := range inherited.Packages {
			out.Packages[n] = pk
		}
		for n, d := range inherited.Defs {
			out.Defs[n] = d
		}
	}
	for name, d := range doc.Packages {
		pk, err := d.pkg(name)
		if err != nil {
			return Proxies{}, err
		}
		out.Packages[name] = pk
	}
	for name, d := range doc.Defs {
		p, err := d.proxy(name)
		if err != nil {
			return Proxies{}, err
		}
		if p.Package != "" {
			if _, ok := out.Packages[p.Package]; !ok {
				return Proxies{}, fmt.Errorf("proxy %q: package %q is not declared in proxies.packages", name, p.Package)
			}
		}
		out.Defs[name] = p
	}
	if err := out.CheckActive(); err != nil {
		return Proxies{}, err
	}
	return out, nil
}

// CheckActive makes sure each active proxy exists and that no two share an
// agent command: the shim would not know which to call.
func (p Proxies) CheckActive() error {
	seen := map[string]bool{}
	byCommand := map[string]string{}
	for _, a := range p.Active {
		switch {
		case a == "":
			return errors.New("proxies.active has an empty entry")
		case seen[a]:
			return fmt.Errorf("proxies.active lists %q twice", a)
		}
		def, ok := p.Defs[a]
		if !ok {
			return fmt.Errorf("proxies.active names %q, which is not defined", a)
		}
		if other, clash := byCommand[def.AgentCommand]; clash {
			return fmt.Errorf("proxies %q and %q are both active and both have agent_command %q; give one a different agent_command", other, a, def.AgentCommand)
		}
		byCommand[def.AgentCommand] = a
		seen[a] = true
	}
	return nil
}

func (d proxyPackageDoc) pkg(name string) (ProxyPackage, error) {
	if !namePattern.MatchString(name) {
		return ProxyPackage{}, fmt.Errorf("proxy package name %q must be lowercase letters, digits, . _ -", name)
	}
	if len(d.Script) == 0 {
		return ProxyPackage{}, fmt.Errorf("proxy package %q has no script", name)
	}
	for _, s := range d.Script {
		if s == "" {
			return ProxyPackage{}, fmt.Errorf("proxy package %q: script has an empty entry", name)
		}
	}
	for k := range d.Vars {
		if !keyPattern.MatchString(k) {
			return ProxyPackage{}, fmt.Errorf("proxy package %q: invalid vars name %q", name, k)
		}
	}
	return ProxyPackage{Script: d.Script, Check: d.Check, Vars: d.Vars}, nil
}

func (d proxyDoc) proxy(name string) (Proxy, error) {
	if !proxyNamePattern.MatchString(name) {
		return Proxy{}, fmt.Errorf("proxy name %q must start with a letter, then lowercase letters, digits or -, at most 24 characters long", name)
	}
	p := Proxy{
		Package: d.Package, Binary: d.Binary, SystemBinary: d.SystemBinary, AgentCommand: d.AgentCommand,
		Description: d.Description, Deny: d.Deny, EnvFrom: d.EnvFrom, RunsIn: d.RunsIn,
	}
	if p.RunsIn == "" {
		p.RunsIn = EmptyDirectory
	}
	if p.RunsIn != EmptyDirectory && p.RunsIn != AgentDirectory {
		return Proxy{}, fmt.Errorf("proxy %q: runs_in must be %q or %q, got %q", name, EmptyDirectory, AgentDirectory, p.RunsIn)
	}
	if p.AgentCommand == "" {
		return Proxy{}, fmt.Errorf("proxy %q: agent_command is required", name)
	}
	if !agentCommandPattern.MatchString(p.AgentCommand) {
		return Proxy{}, fmt.Errorf("proxy %q: agent_command %q must be a bare name the agent can type", name, p.AgentCommand)
	}
	if err := p.checkBinary(name); err != nil {
		return Proxy{}, err
	}
	for _, rule := range p.Deny {
		if len(strings.Fields(rule)) == 0 {
			return Proxy{}, fmt.Errorf("proxy %q: deny has an empty rule", name)
		}
	}
	return p, nil
}

// checkBinary makes sure exactly one way of naming the program is used.
func (p Proxy) checkBinary(name string) error {
	hasPkg := p.Package != "" || p.Binary != ""
	switch {
	case hasPkg && p.SystemBinary != "":
		return fmt.Errorf("proxy %q: set package with binary, or system_binary, not both", name)
	case !hasPkg && p.SystemBinary == "":
		return fmt.Errorf("proxy %q: set package with binary, or system_binary", name)
	case p.SystemBinary != "":
		if !filepath.IsAbs(p.SystemBinary) || filepath.Clean(p.SystemBinary) != p.SystemBinary {
			return fmt.Errorf("proxy %q: system_binary %q must be a clean absolute path", name, p.SystemBinary)
		}
	case p.Package == "":
		return fmt.Errorf("proxy %q: binary needs a package", name)
	case p.Binary == "":
		return fmt.Errorf("proxy %q: package needs a binary", name)
	case !filepath.IsLocal(p.Binary) || filepath.Clean(p.Binary) == ".":
		return fmt.Errorf("proxy %q: binary %q must be a path inside the package, like bin/aws", name, p.Binary)
	}
	return nil
}

// SaveProxiesActive writes proxies.active in the config.yaml in dir. Everything
// else in the file, including comments and the definitions, is kept.
func SaveProxiesActive(dir string, active []string) error {
	path := filepath.Join(dir, BaseFile)
	data, mode, _, err := readOptional(path)
	if err != nil {
		return err
	}
	out, err := editRoot(data, func(root *yaml.Node) error {
		sec := ensureMapping(root, "proxies")
		if sec == nil {
			return errors.New("proxies must be a mapping")
		}
		setList(sec, "active", active)
		return nil
	})
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return writeAtomic(path, out, mode)
}
