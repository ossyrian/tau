package build

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/config"
)

func Assemble(dockerfile string, entries []config.Entry) (string, error) {
	section, err := Render(entries)
	if err != nil {
		return "", err
	}
	return assembleSection(dockerfile, section)
}

func TestAssembleNeedsExactlyOneMarkerLine(t *testing.T) {
	cases := map[string]string{
		"none":      "FROM x\n",
		"two":       "FROM x\n" + Marker + "\n" + Marker + "\n",
		"in text":   "FROM x\nRUN echo " + Marker + "\n",
		"indented":  "FROM x\n  " + Marker + "\n",
		"suffix":    "FROM x\n" + Marker + " extra\n",
		"in a body": "FROM x\nRUN <<'E'\n" + Marker + "\nE\n" + Marker + "\n",
	}
	for name, df := range cases {
		if out, err := Assemble(df, nil); err == nil {
			t.Errorf("%s: want an error, got:\n%s", name, out)
		}
	}
}

func TestAssemblePropagatesRenderErrors(t *testing.T) {
	_, err := Assemble(Marker+"\n", []config.Entry{entry("bad", config.Package{})})
	if err == nil || !strings.Contains(err.Error(), `"bad"`) {
		t.Errorf("got %v", err)
	}
}

func TestDockerArgs(t *testing.T) {
	got := DockerArgs(Options{
		Tag:     "tau:test",
		Hash:    "abc",
		Context: "/ctx",
		Extra:   []string{"--no-cache"},
	})
	want := []string{
		"build", "-f", "-", "-t", "tau:test",
		"--label", LabelConfigHash + "=abc",
		"--no-cache",
		"/ctx",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
