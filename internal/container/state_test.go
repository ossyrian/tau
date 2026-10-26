package container

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type inspectDocker struct {
	out  string
	err  error
	args *[]string
}

func (d inspectDocker) Output(args ...string) (string, error) {
	if d.args != nil {
		*d.args = args
	}
	return d.out, d.err
}

const inspectJSON = `[{
 "Image": "sha256:abc",
 "State": {"Running": true, "StartedAt": "2026-10-23T13:04:12.5Z"},
 "Config": {"Env": ["PATH=/usr/bin", "AWS_REGION=eu-west-1", "EMPTY=", "NOEQUALS"]},
 "Mounts": [
   {"Source": "/h/src/foo", "Destination": "/home/pi/.pi/agent/skills/foo"},
   {"Source": "/h/ws", "Destination": "/workspace"}
 ]}]`

func lc(d Docker) *Lifecycle { return &Lifecycle{D: d, Spec: Spec{Name: "tau"}} }

func TestState(t *testing.T) {
	st, err := lc(inspectDocker{out: inspectJSON}).State()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists || !st.Running || st.ImageID != "sha256:abc" {
		t.Errorf("%+v", st)
	}
	if !st.Started.Equal(time.Date(2026, 10, 23, 13, 4, 12, 500_000_000, time.UTC)) {
		t.Errorf("started %v", st.Started)
	}
	if st.Env["AWS_REGION"] != "eu-west-1" || st.Env["EMPTY"] != "" || len(st.Env) != 3 {
		t.Errorf("env %v", st.Env)
	}
	if _, ok := st.Env["NOEQUALS"]; ok {
		t.Error("kept an entry without '='")
	}
	if st.Mounts["/workspace"] != "/h/ws" || len(st.Mounts) != 2 {
		t.Errorf("mounts %v", st.Mounts)
	}
}

func TestStateOtherDockerFailuresAreErrors(t *testing.T) {
	_, err := lc(inspectDocker{err: errors.New("Cannot connect to the Docker daemon")}).State()
	if err == nil || !strings.Contains(err.Error(), "Docker daemon") {
		t.Errorf("got %v", err)
	}
}

func TestStateRejectsGarbage(t *testing.T) {
	for _, out := range []string{"", "not json", "[]"} {
		if _, err := lc(inspectDocker{out: out}).State(); err == nil {
			t.Errorf("%q accepted", out)
		}
	}
}

func TestMountedFeedsTheSkillsStore(t *testing.T) {
	m, running, err := lc(inspectDocker{out: inspectJSON}).Mounted()
	if err != nil || !running || m["/home/pi/.pi/agent/skills/foo"] != "/h/src/foo" {
		t.Errorf("%v %v %v", m, running, err)
	}
	m, running, err = lc(inspectDocker{err: errors.New("No such object")}).Mounted()
	if err != nil || running || len(m) != 0 {
		t.Errorf("%v %v %v", m, running, err)
	}
}

func TestStateInspectsOnlyContainersNeverTheImageOfTheSameName(t *testing.T) {
	// `docker inspect tau` falls back to the image tau:latest when no
	// container exists, which would report a container that is not there.
	var got []string
	lc(inspectDocker{out: inspectJSON, args: &got}).State()
	if len(got) < 2 || got[0] != "container" || got[1] != "inspect" || got[len(got)-1] != "tau" {
		t.Errorf("args %v", got)
	}
}
