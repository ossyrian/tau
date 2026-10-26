// Package session manages the Pi sessions that run in tmux inside the tau
// container. Each session's tmux name is its identity.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ossyrian/tau/internal/shell"
)

// Docker runs the docker CLI.
type Docker interface {
	// Output runs `docker <args>` and returns its stdout.
	Output(args ...string) (string, error)
	// Run runs `docker <args>` attached to the terminal.
	Run(args ...string) error
}

// Manager runs the `tau sessions` commands for one profile. Every tmux command
// runs as the profile's user, so each profile has its own tmux server and its
// sessions are invisible to the others.
type Manager struct {
	D         Docker
	Container string
	// Profile and User name the profile and its container user.
	Profile, User string
	// PiCmd is the command a new session runs.
	PiCmd string
	// Home is the profile user's home in the container. A new session gets a
	// PATH that holds the shim directory and this home's own bin directories.
	Home string
	// Env returns the profile's variables for a new session, as KEY=VALUE.
	// They reach only sessions started afterwards.
	Env func() ([]string, error)
	// Workspace is the directory a new session starts in. It is also passed to
	// the session as TAU_WORKSPACE, so tools find the profile's workspace.
	Workspace string
	// Rand returns a default session name.
	Rand func() string
	// Loc is the time zone for listed times.
	Loc *time.Location
}

var (
	namePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	// The same socket and config the container's tmux uses everywhere.
	tmuxArgs = []string{"tmux", "-L", "tau", "-f", "/etc/tau/tmux.conf"}
)

// Path is the PATH for a session of the user whose home is home: the proxy
// shims first, then the user's own tools. The image's PATH names /home/pi,
// which another profile's user cannot enter.
func Path(home string) string {
	return "/opt/tau/shims:" + home + "/.local/bin:" + home + "/scripts:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
}

// RandomName returns 8 lowercase hex characters.
func RandomName() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // the OS random source failed; nothing sensible to do
	}
	return hex.EncodeToString(b[:])
}

func (m *Manager) execArgs(flags ...string) []string {
	return append(append([]string{"exec"}, flags...), "-u", m.User, m.Container)
}

func (m *Manager) tmux(sub ...string) (string, error) {
	args := append(m.execArgs(), tmuxArgs...)
	return m.D.Output(append(args, sub...)...)
}

func (m *Manager) attach(name string) error {
	args := append(m.execArgs("-it"), tmuxArgs...)
	return m.D.Run(append(args, "attach", "-t", "="+name)...)
}

func (m *Manager) needRunning() error {
	out, err := m.D.Output("container", "inspect", "-f", "{{.State.Running}}", m.Container)
	if err != nil || strings.TrimSpace(out) != "true" {
		return errors.New("container not running — 'tau start' first")
	}
	return nil
}

// Create starts a detached session running PiCmd and returns its name. An
// empty name picks a random one. It does not attach.
func (m *Manager) Create(name string) (string, error) {
	if err := m.needRunning(); err != nil {
		return "", err
	}
	if name == "" {
		name = m.Rand()
	}
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("session name must be [a-zA-Z0-9_-] (no . or :): %q", name)
	}
	// "=" makes tmux match the name exactly, not as a prefix.
	if _, err := m.tmux("has-session", "-t", "="+name); err == nil {
		return "", fmt.Errorf("session %q already exists", name)
	}
	args := []string{"new-session", "-d", "-s", name}
	if m.Env != nil {
		vars, err := m.Env()
		if err != nil {
			return "", fmt.Errorf("env: %w", err)
		}
		for _, kv := range vars {
			args = append(args, "-e", kv)
		}
	}
	if m.Workspace != "" {
		args = append(args, "-c", m.Workspace, "-e", "TAU_WORKSPACE="+m.Workspace)
	}
	cmd := m.PiCmd
	if m.Home != "" {
		// tmux takes PATH from the process that started its server, here
		// the image's PATH with /home/pi in it, whatever -e says. So the
		// command sets PATH itself.
		// umask 002: the git proxy runs as another user in the workspace's
		// group and must be able to write what the agent wrote.
		cmd = "umask 002 && exec env PATH=" + shell.Quote(Path(m.Home)) + " " + m.PiCmd
	}
	if _, err := m.tmux(append(args, cmd)...); err != nil {
		return "", err
	}
	return name, nil
}

type row struct {
	name              string
	created, activity time.Time
}

func (m *Manager) rows() ([]row, error) {
	out, err := m.tmux("list-sessions", "-F", "#{session_name}|#{session_created}|#{session_activity}")
	if err != nil {
		return nil, nil // tmux exits non-zero when it has no server: no sessions
	}
	var rows []row
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "|")
		if len(f) != 3 {
			continue
		}
		c, err1 := strconv.ParseInt(f[1], 10, 64)
		a, err2 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		rows = append(rows, row{f[0], time.Unix(c, 0).In(m.Loc), time.Unix(a, 0).In(m.Loc)})
	}
	return rows, nil
}

// Info describes one session.
type Info struct {
	Name string
	// Profile is the profile that owns the session.
	Profile string
	// Kind is "subagent" for a name starting "sa-" (Pi's naming), else "session".
	Kind            string
	Created, Active time.Time
}

// Sessions returns the sessions in tmux's order. No sessions is not an error.
func (m *Manager) Sessions() ([]Info, error) {
	if err := m.needRunning(); err != nil {
		return nil, err
	}
	rows, _ := m.rows()
	infos := make([]Info, 0, len(rows))
	for _, r := range rows {
		kind := "session"
		if strings.HasPrefix(r.name, "sa-") {
			kind = "subagent"
		}
		infos = append(infos, Info{Name: r.name, Profile: m.Profile, Kind: kind, Created: r.created, Active: r.activity})
	}
	return infos, nil
}

// previewScript prints "@@@name" and then the text of the session's pane, for
// each name in its arguments. Names are arguments, never part of the script.
const previewScript = `for s in "$@"; do
  printf '@@@%s\n' "$s"
  tmux -L tau -f /etc/tau/tmux.conf capture-pane -p -t "=$s:" 2>/dev/null
done`

// Preview returns the last n non-blank lines on each session's screen, in one
// docker call.
func (m *Manager) Preview(n int) (map[string][]string, error) {
	infos, err := m.Sessions()
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	if len(infos) == 0 {
		return out, nil
	}
	args := append(m.execArgs(), "sh", "-c", previewScript, "sh")
	for _, i := range infos {
		args = append(args, i.Name)
	}
	text, err := m.D.Output(args...)
	if err != nil {
		return nil, err
	}
	var name string
	var lines []string
	flush := func() {
		if name == "" {
			return
		}
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
		for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
			lines = lines[1:]
		}
		out[name] = lines
	}
	for _, l := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(l, "@@@"); ok {
			flush()
			name, lines = rest, nil
			continue
		}
		lines = append(lines, strings.TrimRight(l, " \t\r"))
	}
	flush()
	return out, nil
}

// resolve returns the session named q: an exact match, else a unique prefix.
func (m *Manager) resolve(q string) (string, error) {
	rows, _ := m.rows()
	if len(rows) == 0 {
		return "", errors.New("no active sessions")
	}
	var matches []string
	for _, r := range rows {
		if r.name == q {
			return q, nil
		}
		if strings.HasPrefix(r.name, q) {
			matches = append(matches, r.name)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no session matching '%s'", q)
	case 1:
		return matches[0], nil
	}
	return "", fmt.Errorf("ambiguous '%s' — matches multiple sessions", q)
}

// Kill ends the session matching name.
func (m *Manager) Kill(name string) (string, error) {
	if err := m.needRunning(); err != nil {
		return "", err
	}
	if name == "" {
		return "", errors.New("usage: tau sessions kill <name>")
	}
	full, err := m.resolve(name)
	if err != nil {
		return "", err
	}
	if _, err := m.tmux("kill-session", "-t", "="+full); err != nil {
		return "", err
	}
	return full, nil
}

// AttachArgs resolves name and returns the docker arguments that attach a terminal to that session. A caller that owns the
// terminal, like the TUI, runs them itself.
func (m *Manager) AttachArgs(name string) ([]string, error) {
	if err := m.needRunning(); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, errors.New("usage: tau sessions attach <name>")
	}
	full, err := m.resolve(name)
	if err != nil {
		return nil, err
	}
	return append(append(m.execArgs("-it"), tmuxArgs...), "attach", "-t", "="+full), nil
}

// Attach connects the terminal to the session matching name.
func (m *Manager) Attach(name string) error {
	args, err := m.AttachArgs(name)
	if err != nil {
		return err
	}
	return m.D.Run(args...)
}

// ShellArgs returns the docker arguments for an interactive zsh as the
// profile's user, in the directory and with the PATH and variables a new
// session gets.
func (m *Manager) ShellArgs() ([]string, error) {
	if err := m.needRunning(); err != nil {
		return nil, err
	}
	flags := []string{"-it"}
	if m.Env != nil {
		vars, err := m.Env()
		if err != nil {
			return nil, fmt.Errorf("env: %w", err)
		}
		for _, kv := range vars {
			flags = append(flags, "-e", kv)
		}
	}
	if m.Home != "" {
		flags = append(flags, "-e", "PATH="+Path(m.Home))
	}
	if m.Workspace != "" {
		flags = append(flags, "-w", m.Workspace, "-e", "TAU_WORKSPACE="+m.Workspace)
	}
	// umask 002, as in Create: the git proxy must be able to write what the
	// shell writes.
	return append(m.execArgs(flags...), "sh", "-c", "umask 002 && exec zsh"), nil
}

// Shell opens an interactive zsh in the container.
func (m *Manager) Shell() error {
	args, err := m.ShellArgs()
	if err != nil {
		return err
	}
	return m.D.Run(args...)
}

// AttachNew connects the terminal to the session just made by Create.
func (m *Manager) AttachNew(name string) error {
	return m.attach(name)
}
