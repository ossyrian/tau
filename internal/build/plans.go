package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/profile"
)

// ProfilePlan is one profile with the packages it installs.
type ProfilePlan struct {
	Profile profile.Profile
	Entries []config.Entry
	// SharedWorkspace puts the profile's user in SharedGroup, which can reach
	// the shared workspace. The default profile is always in it (see the Dockerfile).
	SharedWorkspace bool
}

// SharedGroup owns the directory that holds the shared workspace.
const SharedGroup = "tau-shared"

// RenderPlans returns the Dockerfile section for every profile, the default
// profile first. All profiles share one image: a named profile gets its own
// user, and its "as: user" packages run as that user. "as: root" packages
// install system-wide, so every profile sees them.
//
// With only the default profile the result is exactly Render's.
func RenderPlans(plans []ProfilePlan) (string, error) {
	var b strings.Builder
	for _, p := range plans {
		if p.Profile.Name != profile.Default {
			b.WriteString(userSection(p))
		}
	}
	for _, p := range plans {
		user, markers := p.Profile.User, MarkerDir
		if p.Profile.Name != profile.Default {
			markers = path.Join(MarkerDir, user)
		}
		if err := renderFor(&b, user, markers, p.Entries); err != nil {
			if p.Profile.Name == profile.Default {
				return "", err
			}
			return "", fmt.Errorf("profile %q: %w", p.Profile.Name, err)
		}
	}
	b.WriteString("USER pi\n")
	return b.String(), nil
}

// userSection creates a profile's user. The home is mode 700, so no other
// profile's user, and not the agent of another profile, can read into it.
func userSection(p ProfilePlan) string {
	h, u := p.Profile.Home(), p.Profile.User
	shared := ""
	if p.SharedWorkspace {
		shared = fmt.Sprintf(" && usermod -aG %s %s", SharedGroup, u)
	}
	return fmt.Sprintf(`USER root
RUN useradd -m -u %[1]d -d %[2]s -s /bin/zsh -G tau-clients %[3]s && chmod 700 %[2]s%[4]s && \
    mkdir -p %[2]s/.treehouse %[2]s/share %[2]s/.config/tau && \
    ln -s %[2]s/.pi/agent/treehouse %[2]s/.config/treehouse && \
    chown -R %[3]s:%[3]s %[2]s
COPY --chown=%[1]d:%[1]d zshrc.base %[2]s/.zshrc
`, p.Profile.UID, h, u, shared)
}

// PlansHash identifies the whole build: every profile and every package in it.
// With only the default profile it equals ConfigHash of its entries, so an
// image built before profiles existed still counts as current.
func PlansHash(plans []ProfilePlan) string {
	if len(plans) == 1 && plans[0].Profile.Name == profile.Default {
		return ConfigHash(plans[0].Entries)
	}
	h := sha256.New()
	fmt.Fprintf(h, "profiles=%d;", len(plans))
	for _, p := range plans {
		fmt.Fprintf(h, "%d:%s/%d/%t=%s;", len(p.Profile.Name), p.Profile.Name, p.Profile.UID, p.SharedWorkspace, ConfigHash(p.Entries))
	}
	return hex.EncodeToString(h.Sum(nil))
}
