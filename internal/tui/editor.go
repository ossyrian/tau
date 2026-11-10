package tui

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

// editorCmd opens path in the user's editor: $VISUAL, then $EDITOR, then vi.
// The variable may carry arguments ("code --wait"), so it is split on spaces.
func editorCmd(path string) (*exec.Cmd, error) {
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if f := strings.Fields(os.Getenv(name)); len(f) > 0 {
			return exec.Command(f[0], append(f[1:], path)...), nil
		}
	}
	if _, err := exec.LookPath("vi"); err != nil {
		return nil, errors.New("no editor: set $EDITOR")
	}
	return exec.Command("vi", path), nil
}
