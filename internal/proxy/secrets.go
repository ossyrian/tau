package proxy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ossyrian/tau/internal/envfile"
)

// Var is one secret or setting of a proxy.
type Var = envfile.Var

var (
	proxyName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	keyName   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Secrets keeps each proxy's variables in <Dir>/<proxy>.env on the host. The
// files are mode 0600 and never leave the host except over the server's stdin.
type Secrets struct {
	Dir string
	// Run runs a shell command on the host and returns its stdout. It mints
	// credentials for a proxy's env_from.
	Run func(cmd string) (string, error)
}

func (s *Secrets) path(proxy string) (string, error) {
	if !proxyName.MatchString(proxy) {
		return "", fmt.Errorf("proxy name %q must be lowercase letters, digits, . _ -", proxy)
	}
	return filepath.Join(s.Dir, proxy+".env"), nil
}

// Vars lists a proxy's variables in file order. A proxy with no file has none.
func (s *Secrets) Vars(proxy string) ([]Var, error) {
	p, err := s.path(proxy)
	if err != nil {
		return nil, err
	}
	return envfile.List(p)
}

// Set adds or replaces one variable. A replaced key keeps its place.
func (s *Secrets) Set(proxy, key, value string) error {
	p, err := s.path(proxy)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	return envfile.Set(p, key, value)
}

// Unset removes one variable and reports whether it was there.
func (s *Secrets) Unset(proxy, key string) (bool, error) {
	p, err := s.path(proxy)
	if err != nil {
		return false, err
	}
	return envfile.Unset(p, key)
}

// Mask hides all of a value but its last four characters.
var Mask = envfile.Mask

// MergeEnv combines stored variables with minted ones. Minted values win, so
// a fresh credential replaces a stale stored one.
func MergeEnv(stored, minted []Var) map[string]string {
	out := map[string]string{}
	for _, v := range stored {
		out[v.Key] = v.Value
	}
	for _, v := range minted {
		out[v.Key] = v.Value
	}
	return out
}

// ParseMinted reads KEY=VALUE lines, with or without a leading "export ".
// Values are taken literally and never evaluated. Other lines are ignored.
func ParseMinted(out string) []Var {
	var vars []Var
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok || !keyName.MatchString(k) {
			continue
		}
		vars = append(vars, Var{Key: k, Value: v})
	}
	return vars
}

// Mint runs a proxy's env_from command on the host and returns the variables
// it prints. An empty command mints nothing.
func (s *Secrets) Mint(cmd string) ([]Var, error) {
	if cmd == "" {
		return nil, nil
	}
	if s.Run == nil {
		return nil, errors.New("cannot run env_from here")
	}
	out, err := s.Run(cmd)
	if err != nil {
		return nil, fmt.Errorf("env_from: %w", err)
	}
	return ParseMinted(out), nil
}
