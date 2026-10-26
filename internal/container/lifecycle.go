package container

import (
	"fmt"
	"io"
	"strings"
)

// Docker runs the docker CLI and returns its stdout.
type Docker interface {
	Output(args ...string) (string, error)
}

// Outcome is what a lifecycle command did. The caller words it for its reader.
type Outcome string

const (
	Started        Outcome = "started"
	AlreadyRunning Outcome = "already-running"
	Stopped        Outcome = "stopped"
	NotRunning     Outcome = "not-running"
	Restarted      Outcome = "restarted"
)

// Text is the outcome as a person reads it, and a hint to go with it.
func (o Outcome) Text() (text, hint string) {
	switch o {
	case AlreadyRunning:
		return "already running", ""
	case NotRunning:
		return "not running", ""
	case Stopped:
		return "stopped", "sessions lost"
	case Restarted:
		return "restarted", "previous sessions lost"
	}
	return string(o), ""
}

// Lifecycle starts, stops and restarts the tau container.
type Lifecycle struct {
	D    Docker
	Spec Spec
	// Repo is the tau checkout, the source of pi-config.
	Repo   string
	Stderr io.Writer // warnings
	// Tracked lists the files git tracks under a directory.
	Tracked func(dir string) ([]string, error)
	// After runs once the container is up: it starts the proxy servers. A
	// failure is reported but the container stays, because it is usable
	// without proxies and `tau proxy reload` retries.
	After func() error
	// Members, when set, lists the profiles at each start, so a profile added
	// since tau began is included. It replaces Spec.Profiles.
	Members func() ([]Member, error)
	// Skills resolves a profile's active skills at each start. Warnings are
	// printed. An error stops the start: a broken config must not start the
	// agent without the skills the user chose.
	Skills func(profile string) ([]SkillMount, []string, error)
}

func (l *Lifecycle) exists() bool {
	_, err := l.D.Output("container", "inspect", l.Spec.Name)
	return err == nil
}

// Running reports whether the container exists and is running.
func (l *Lifecycle) Running() bool {
	out, err := l.D.Output("container", "inspect", "-f", "{{.State.Running}}", l.Spec.Name)
	return err == nil && strings.TrimSpace(out) == "true"
}

// create always makes a fresh container. `docker start` would reuse the old
// settings and miss changes to .env, mounts and ports, and a stopped container
// holds no sessions or credentials worth keeping.
func (l *Lifecycle) create() error {
	// Resolve first: a broken config must not remove a working container.
	spec := l.Spec
	spec.Profiles = append([]Member(nil), l.Spec.Profiles...)
	if l.Members != nil {
		members, err := l.Members()
		if err != nil {
			return fmt.Errorf("profiles: %w", err)
		}
		spec.Profiles = members
	}
	if l.Skills != nil {
		for i, m := range spec.Profiles {
			mounts, warnings, err := l.Skills(m.Profile.Name)
			if err != nil {
				return fmt.Errorf("skills (%s): %w", m.Profile.Name, err)
			}
			for _, w := range warnings {
				fmt.Fprintf(l.Stderr, "tau: %s\n", w)
			}
			spec.Profiles[i].Skills = mounts
		}
	}
	if l.exists() {
		if _, err := l.D.Output("rm", "-f", l.Spec.Name); err != nil {
			return err
		}
	}
	if err := Prepare(spec, l.Repo, l.Tracked); err != nil {
		return err
	}
	if _, err := l.D.Output(RunArgs(spec, l.Stderr)...); err != nil {
		return fmt.Errorf("docker run: %w", err)
	}
	if l.After != nil {
		if err := l.After(); err != nil {
			return fmt.Errorf("container started, but the proxy servers did not: %w (tau proxy reload retries)", err)
		}
	}
	return nil
}

// Start creates and starts the container unless it is already running.
func (l *Lifecycle) Start() (Outcome, error) {
	if l.Running() {
		return AlreadyRunning, nil
	}
	if err := l.create(); err != nil {
		return "", err
	}
	return Started, nil
}

// Stop removes the container. Its sessions are lost.
func (l *Lifecycle) Stop() (Outcome, error) {
	if !l.exists() {
		return NotRunning, nil
	}
	if _, err := l.D.Output("rm", "-f", l.Spec.Name); err != nil {
		return "", err
	}
	return Stopped, nil
}

// Restart recreates the container if it is running; otherwise it does nothing.
func (l *Lifecycle) Restart() (Outcome, error) {
	if !l.Running() {
		return NotRunning, nil
	}
	if err := l.create(); err != nil {
		return "", err
	}
	return Restarted, nil
}
