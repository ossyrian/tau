package hostclip

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// xclip runs the container's stand-in with a home that holds image, if any.
func xclip(t *testing.T, image string, args ...string) (string, error) {
	t.Helper()
	home := t.TempDir()
	if image != "" {
		dir := filepath.Join(home, "share", "clipboard")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, File), []byte(image), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("../../tools/clipboard/xclip", args...)
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	out, err := cmd.Output()
	return string(out), err
}

func TestXclipListsAndReadsTheImage(t *testing.T) {
	out, err := xclip(t, "PNGDATA", "-selection", "clipboard", "-t", "TARGETS", "-o")
	if err != nil || strings.TrimSpace(out) != "image/png" {
		t.Errorf("targets: %q %v", out, err)
	}
	out, err = xclip(t, "PNGDATA", "-selection", "clipboard", "-t", "image/png", "-o")
	if err != nil || out != "PNGDATA" {
		t.Errorf("image: %q %v", out, err)
	}
}

func TestXclipFailsWithoutAnImageOrForOtherCalls(t *testing.T) {
	for name, c := range map[string]struct {
		image string
		args  []string
	}{
		"no image, targets": {"", []string{"-selection", "clipboard", "-t", "TARGETS", "-o"}},
		"no image, png":     {"", []string{"-selection", "clipboard", "-t", "image/png", "-o"}},
		"other type":        {"PNGDATA", []string{"-selection", "clipboard", "-t", "image/jpeg", "-o"}},
		"copy":              {"PNGDATA", []string{"-selection", "clipboard"}},
	} {
		if out, err := xclip(t, c.image, c.args...); err == nil || out != "" {
			t.Errorf("%s: out %q err %v, want a silent failure", name, out, err)
		}
	}
}
