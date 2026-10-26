package container

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// State is what `docker inspect` reports about the container.
type State struct {
	Exists, Running bool
	Started         time.Time
	ImageID         string
	Env             map[string]string // the container's environment
	Mounts          map[string]string // destination -> host source
}

// State inspects the container. A container that does not exist is not an
// error; a Docker that cannot be reached is.
func (l *Lifecycle) State() (State, error) {
	out, err := l.D.Output("container", "inspect", l.Spec.Name)
	if err != nil {
		if strings.Contains(err.Error(), "No such") {
			return State{}, nil
		}
		return State{}, err
	}
	var docs []struct {
		Image string
		State struct {
			Running   bool
			StartedAt time.Time
		}
		Config struct{ Env []string }
		Mounts []struct{ Source, Destination string }
	}
	if err := json.Unmarshal([]byte(out), &docs); err != nil {
		return State{}, fmt.Errorf("docker inspect: %w", err)
	}
	if len(docs) != 1 {
		return State{}, errors.New("docker inspect: unexpected output")
	}
	d := docs[0]
	st := State{
		Exists: true, Running: d.State.Running, Started: d.State.StartedAt, ImageID: d.Image,
		Env: map[string]string{}, Mounts: map[string]string{},
	}
	for _, kv := range d.Config.Env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			st.Env[k] = v
		}
	}
	for _, m := range d.Mounts {
		st.Mounts[m.Destination] = m.Source
	}
	return st, nil
}

// Mounted returns the container's mounts and whether it is running, in the
// form skills.Store needs.
func (l *Lifecycle) Mounted() (map[string]string, bool, error) {
	st, err := l.State()
	return st.Mounts, st.Running, err
}
