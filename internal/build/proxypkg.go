package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	"maps"
	"slices"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/proxy"
	"github.com/ossyrian/tau/internal/shell"
)

// proxyMarkerDir holds one file per proxy package, owned by root.
const proxyMarkerDir = "/etc/tau/proxy-installed"

// binariesOf lists, for each package, the files proxies run from it.
func binariesOf(p config.Proxies) map[string][]string {
	out := map[string][]string{}
	for _, name := range p.Names() {
		d := p.Defs[name]
		if d.Package == "" {
			continue
		}
		bin := path.Join(proxy.PackagePrefix(d.Package), d.Binary)
		if !hasString(out[d.Package], bin) {
			out[d.Package] = append(out[d.Package], bin)
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

func hasString(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// RenderProxyPackages returns the Dockerfile section that installs the hidden
// packages, or "" when there are none. Each is installed as root into its own
// prefix under proxy.PackageDir, which only the proxy group can enter, so the
// agent cannot run or read the software. The script gets the prefix in $PREFIX.
// The build fails if a proxy's binary is missing once the install is done.
func RenderProxyPackages(p config.Proxies) (string, error) {
	names := p.PackageNames()
	if len(names) == 0 {
		return "", nil
	}
	bins := binariesOf(p)
	var b strings.Builder
	b.WriteString("USER root\n")
	fmt.Fprintf(&b, "RUN install -d -m 750 -o root -g %s %s %s\n", proxy.PackagesGroup, proxy.PackageDir, proxyMarkerDir)
	for _, n := range names {
		block, err := renderProxyPackage(n, p.Packages[n], bins[n])
		if err != nil {
			return "", fmt.Errorf("proxy package %q: %w", n, err)
		}
		b.WriteString(block)
	}
	return b.String(), nil
}

func renderProxyPackage(name string, pk config.ProxyPackage, bins []string) (string, error) {
	for _, s := range pk.Script {
		if strings.ContainsRune(s, 0) {
			return "", fmt.Errorf("script contains a NUL byte")
		}
	}
	if strings.ContainsRune(pk.Check, 0) {
		return "", fmt.Errorf("check contains a NUL byte")
	}
	for k, v := range pk.Vars {
		if strings.ContainsRune(v, 0) {
			return "", fmt.Errorf("vars.%s contains a NUL byte", k)
		}
	}
	prefix := proxy.PackagePrefix(name)
	var body strings.Builder
	// Files the installer makes are closed to others from the start.
	body.WriteString("umask 027\n")
	fmt.Fprintf(&body, "export PREFIX=%s\n", shell.Quote(prefix))
	for _, k := range slices.Sorted(maps.Keys(pk.Vars)) {
		fmt.Fprintf(&body, "export %s=%s\n", k, shell.Quote(pk.Vars[k]))
	}
	fmt.Fprintf(&body, "mkdir -p \"$PREFIX\"\n")
	var install strings.Builder
	for _, s := range pk.Script {
		install.WriteString("(\n" + strings.TrimRight(s, "\n") + "\n)\n")
	}
	if pk.Check != "" {
		fmt.Fprintf(&body, "if (\n%s\n) >/dev/null 2>&1; then\necho \"tau: proxy package %s: already installed, skipping\"\nelse\n%sfi\n", pk.Check, name, install.String())
	} else {
		body.WriteString(install.String())
	}
	// Whatever the installer did, close the tree to everyone but root and the proxy group.
	fmt.Fprintf(&body, "chown -R root:%s \"$PREFIX\"\nchmod -R o-rwx \"$PREFIX\"\nchmod 750 %s\n", proxy.PackagesGroup, shell.Quote(prefix))
	for _, bin := range bins {
		fmt.Fprintf(&body, "test -x %s || { echo \"tau: proxy package %s: %s is missing or not executable\" >&2; exit 1; }\n", shell.Quote(bin), name, bin)
	}
	fmt.Fprintf(&body, "printf '%%s\\n' '%s' > %s/%s\n", proxyPackageHash(name, pk, bins), proxyMarkerDir, name)

	text := body.String()
	delim := delimiterFor(text)
	return fmt.Sprintf("# proxy package: %s\nRUN bash -eo pipefail <<'%s'\n%s%s\n", name, delim, text, delim), nil
}

func proxyPackageHash(name string, pk config.ProxyPackage, bins []string) string {
	h := sha256.New()
	field := func(tag, s string) { fmt.Fprintf(h, "%s%d:%s;", tag, len(s), s) }
	field("name", name)
	field("check", pk.Check)
	fmt.Fprintf(h, "script[%d]", len(pk.Script))
	for _, s := range pk.Script {
		field("i", s)
	}
	fmt.Fprintf(h, "vars{%d}", len(pk.Vars))
	for _, k := range slices.Sorted(maps.Keys(pk.Vars)) {
		field("k", k)
		field("v", pk.Vars[k])
	}
	fmt.Fprintf(h, "bins[%d]", len(bins))
	for _, b := range bins {
		field("b", b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ProxyHash identifies everything the hidden packages install: each package's
// script, check and vars, and the binaries proxies run from it. It is "" when
// there are none, so an image without proxy packages keeps its old hash.
// Descriptions, deny rules, env_from, the active list and system binaries do
// not install anything, so changing them does not make the image stale.
func ProxyHash(p config.Proxies) string {
	names := p.PackageNames()
	if len(names) == 0 {
		return ""
	}
	bins := binariesOf(p)
	h := sha256.New()
	for _, n := range names {
		fmt.Fprintf(h, "%d:%s=%s;", len(n), n, proxyPackageHash(n, p.Packages[n], bins[n]))
	}
	return hex.EncodeToString(h.Sum(nil))
}
