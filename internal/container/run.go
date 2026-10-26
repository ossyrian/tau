package container

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ossyrian/tau/internal/profile"
)

// SharedWorkspace is where the shared workspace is mounted. Its parent is
// readable only by the group tau-shared, so a profile with an isolated
// workspace cannot see it. /workspace links here.
const SharedWorkspace = "/srv/shared/workspace"

// Member is one profile in the container.
type Member struct {
	Profile profile.Profile
	// Workspace is the host directory this profile mounts at WorkspaceDest.
	// It is empty when the profile uses the shared workspace, which the
	// default profile mounts.
	Workspace     string
	WorkspaceDest string
	// Live maps a name to a host directory, mounted at WorkspaceDest/<name>.
	Live map[string]string
	// Skills are the active skills to mount; the caller resolves them.
	Skills []SkillMount
}

// Shared reports whether the member sees the shared workspace.
func (m Member) Shared() bool { return m.WorkspaceDest == SharedWorkspace }

// Spec holds the host paths and settings that go into `docker run`.
type Spec struct {
	Name, Image, Ports string
	Home               string // host home directory
	// Profiles lists every profile, the default first.
	Profiles []Member
}

// RunArgs returns the arguments for `docker run`. Warnings about skipped
// mounts go to warn. The container gets no environment from the host and no
// credentials: those live in proxies, outside the agent's reach. Mount order
// matters: live workspaces come after the workspace mount so they sit on top
// of it.
func RunArgs(s Spec, warn io.Writer) []string {
	// no-new-privileges: a setuid binary cannot raise a process above its user,
	// so one profile's user cannot become another's.
	a := []string{"run", "-d", "--name", s.Name, "--security-opt", "no-new-privileges", "-e", "TAU_PORTS=" + s.Ports}
	a = append(a, PortArgs(s.Ports)...)
	for _, m := range s.Profiles {
		a = append(a, memberArgs(m, s.Home, warn)...)
	}
	return append(a, s.Image)
}

func memberArgs(m Member, homeOf string, warn io.Writer) []string {
	p, home := m.Profile, m.Profile.Home()
	a := []string{
		"-v", p.Treehouse() + ":" + home + "/.treehouse",
		"-v", p.Scripts() + ":" + home + "/scripts",
		"-v", p.Share() + ":" + home + "/share",
		"-v", p.PiDir() + ":" + home + "/.pi",
	}
	if m.Workspace != "" {
		a = append(a, "-v", m.Workspace+":"+m.WorkspaceDest)
	}
	if exists(p.GitConfig()) {
		a = append(a, "-v", p.GitConfig()+":"+home+"/.gitconfig:ro")
	}
	if exists(p.Zshrc()) {
		a = append(a, "-v", p.Zshrc()+":"+home+"/.config/tau/zshrc")
	}
	if exists(p.Vimrc()) {
		a = append(a, "-v", p.Vimrc()+":"+home+"/.vimrc")
	}
	a = append(a, SkillArgs(home, m.Skills)...)
	return append(a, LiveWorkspaceMounts(m.Live, homeOf, m.WorkspaceDest, warn)...)
}

func exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// Prepare creates the host directories every profile mounts and refreshes the tau-standard Pi
// config of each profile. tracked lists the files under a directory that
// belong to tau; only those are copied, and they overwrite the runtime copies.
// Files the user owns are never touched.
func Prepare(s Spec, repo string, tracked func(dir string) ([]string, error)) error {
	for _, m := range s.Profiles {
		p := m.Profile
		dirs := []string{
			p.Scripts(), p.Share(), p.Treehouse(),
			filepath.Join(p.PiDir(), "agent", "skills"),
			filepath.Join(p.PiDir(), "agent", "treehouse"),
		}
		if m.Workspace != "" {
			dirs = append(dirs, m.Workspace)
		}
		for _, d := range dirs {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
		if err := seedPiConfig(p.PiDir(), filepath.Join(repo, "pi-config"), tracked); err != nil {
			return err
		}
	}
	return nil
}

func seedPiConfig(dst, src string, tracked func(string) ([]string, error)) error {
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		return nil
	}
	if mustAbs(src) == mustAbs(dst) {
		return nil
	}
	files, err := tracked(src)
	if err != nil {
		return err
	}
	for _, rel := range files {
		clean := filepath.Clean(rel)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("refusing tracked path outside pi-config: %q", rel)
		}
		from := filepath.Join(src, clean)
		data, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		st, err := os.Stat(from)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, clean)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(out, data, st.Mode().Perm()); err != nil {
			return err
		}
		if err := os.Chmod(out, st.Mode().Perm()); err != nil { // WriteFile keeps the mode of an existing file
			return err
		}
	}
	return nil
}

// mustAbs returns p as an absolute path with symlinks resolved, or p itself
// when that fails.
func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if real, err := filepath.EvalSymlinks(a); err == nil {
		return real
	}
	return a
}
