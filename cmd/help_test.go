package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestEveryCommandHasADescription(t *testing.T) {
	h := newHarness(t, "", "")
	var check func(prefix string, subs []*cobra.Command)
	check = func(prefix string, subs []*cobra.Command) {
		for _, c := range subs {
			if c.Short == "" {
				t.Errorf("%s%s has no description", prefix, c.Name())
			}
			check(prefix+c.Name()+" ", c.Commands())
		}
	}
	check("", h.app.NewRootCmd().Commands())
}

func TestEnvIsHiddenFromTheTopLevelHelp(t *testing.T) {
	h := newHarness(t, "", "")
	h.app.Run([]string{"-h"})
	for _, l := range strings.Split(h.out.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "env ") {
			t.Errorf("top-level help lists env: %q", l)
		}
	}
}

func TestAWrongCallExitsOneWithTheErrorOnStderr(t *testing.T) {
	h := newHarness(t, "", "")
	if code := h.app.Run([]string{"build", "--bogus"}); code != 1 {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(h.err.String(), "unknown flag: --bogus") || h.out.Len() != 0 {
		t.Errorf("out %q err %q", h.out, h.err)
	}
}
