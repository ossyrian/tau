package build

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/profile"
	"github.com/ossyrian/tau/internal/proxy"
)

// Plan is everything a build needs, worked out from a config.
type Plan struct {
	Dockerfile string
	Entries    []config.Entry
	Hash       string // ConfigHash of Entries
}

// ImageStatus compares the built image with a Plan.
type ImageStatus int

const (
	ImageUnknown ImageStatus = iota // not checked, or the check failed
	ImageMissing                    // no image with the tag
	ImageStale                      // built from a different plan, or by plain `docker build`
	ImageCurrent
)

func (s ImageStatus) String() string {
	return [...]string{"unknown", "missing", "stale", "current"}[s]
}

// Docker reads from the docker CLI.
type Docker interface {
	Output(args ...string) (string, error)
}

// Builder plans and runs image builds.
type Builder struct {
	Repo string // directory holding the Dockerfile; also the build context
	Tag  string // image name and tag
	D    Docker // for Status
	// Run runs `docker <args>` with dockerfile on stdin, writing progress to out.
	Run func(args []string, dockerfile string, out io.Writer) error
}

// ProfileConfig is a profile with its loaded config.
type ProfileConfig struct {
	Profile profile.Profile
	Config  *config.Config
}

// Plan reads the Dockerfile and renders every profile's packages into it.
// The default profile must come first.
func (b *Builder) Plan(profiles []ProfileConfig) (Plan, error) {
	raw, err := os.ReadFile(filepath.Join(b.Repo, "Dockerfile"))
	if err != nil {
		return Plan{}, fmt.Errorf("%w (set --repo or TAU_REPO to the tau checkout)", err)
	}
	var plans []ProfilePlan
	for _, p := range profiles {
		entries, err := p.Config.Resolve()
		if err != nil {
			if p.Profile.Name == profile.Default {
				return Plan{}, err
			}
			return Plan{}, fmt.Errorf("profile %q: %w", p.Profile.Name, err)
		}
		ws, err := config.LoadWorkspaces(p.Config.Dir)
		if err != nil {
			return Plan{}, err
		}
		plans = append(plans, ProfilePlan{Profile: p.Profile, Entries: entries, SharedWorkspace: ws.Mode == config.WorkspaceShared})
	}
	section, err := RenderPlans(plans)
	if err != nil {
		return Plan{}, err
	}
	px, err := b.proxies(profiles)
	if err != nil {
		return Plan{}, err
	}
	hidden, err := RenderProxyPackages(px)
	if err != nil {
		return Plan{}, err
	}
	// The hidden packages install as root; the section must end as the default user.
	section = strings.TrimSuffix(section, "USER pi\n") + hidden + "USER pi\n"
	df, err := assembleSection(string(raw), section)
	if err != nil {
		return Plan{}, err
	}
	var all []config.Entry
	for _, p := range plans {
		all = append(all, p.Entries...)
	}
	hash := PlansHash(plans)
	if ph := ProxyHash(px); ph != "" {
		sum := sha256.Sum256([]byte(hash + "|proxy|" + ph))
		hash = hex.EncodeToString(sum[:])
	}
	return Plan{Dockerfile: df, Entries: all, Hash: hash}, nil
}

// proxies reads the default profile's proxy packages and definitions, which
// every profile shares, and checks that each can have a server.
func (b *Builder) proxies(profiles []ProfileConfig) (config.Proxies, error) {
	for _, p := range profiles {
		if p.Profile.Name != profile.Default {
			continue
		}
		px, err := config.LoadProxies(p.Config.Dir)
		if err != nil {
			return config.Proxies{}, err
		}
		if err := (proxy.Host{Proxies: px}).Check(); err != nil {
			return config.Proxies{}, err
		}
		return px, nil
	}
	return config.Proxies{}, nil
}

// Build runs the plan. Progress goes to out.
func (b *Builder) Build(p Plan, noCache bool, out io.Writer) error {
	if b.Run == nil {
		return errors.New("building is not available")
	}
	o := Options{Tag: b.Tag, Hash: p.Hash, Context: b.Repo}
	if noCache {
		o.Extra = append(o.Extra, "--no-cache")
	}
	return b.Run(DockerArgs(o), p.Dockerfile, out)
}

// Status compares the image's recorded plan hash with hash.
func (b *Builder) Status(hash string) (ImageStatus, error) {
	label := `{{index .Config.Labels "` + LabelConfigHash + `"}}`
	out, err := b.D.Output("image", "inspect", "-f", label, b.Tag)
	if err != nil {
		if strings.Contains(err.Error(), "No such") {
			return ImageMissing, nil
		}
		return ImageUnknown, err
	}
	if strings.TrimSpace(out) == hash {
		return ImageCurrent, nil
	}
	return ImageStale, nil
}
