package container

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPortArgsAlwaysBindLoopback(t *testing.T) {
	for _, a := range PortArgs("1,2-3,4") {
		if a != "-p" && !strings.HasPrefix(a, "127.0.0.1:") {
			t.Errorf("port published beyond loopback: %s", a)
		}
	}
}

func TestPortArgsRejectsGarbage(t *testing.T) {
	// Anything that is not a port or a port range could smuggle extra docker syntax.
	for _, bad := range []string{"80;rm", "0.0.0.0:80", "a-b", "80:80", "-1", "99999", "8-", "9-8", "0", "0-5", "65536", "1-65536"} {
		if got := PortArgs(bad); got != nil {
			t.Errorf("%q accepted: %v", bad, got)
		}
	}
}

func TestLiveWorkspaceMounts(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "proj")
	os.MkdirAll(src, 0o755)
	var warn strings.Builder
	got := LiveWorkspaceMounts(map[string]string{"live1": src, "gone": filepath.Join(d, "nope")}, d, "/workspace", &warn)
	want := []string{"-v", src + ":/workspace/live1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	if !strings.Contains(warn.String(), "gone") || strings.Contains(warn.String(), "live1") {
		t.Errorf("warn %q", warn.String())
	}
}

func TestALiveSourceStartingWithTildeIsTheUsersHome(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, "dev", "app")
	os.MkdirAll(src, 0o755)
	got := LiveWorkspaceMounts(map[string]string{"app": "~/dev/app"}, home, "/workspace", &strings.Builder{})
	if !reflect.DeepEqual(got, []string{"-v", src + ":/workspace/app"}) {
		t.Errorf("got %v", got)
	}
}

func TestLiveWorkspaceNamesCannotEscapeWorkspace(t *testing.T) {
	d := t.TempDir()
	var warn strings.Builder
	got := LiveWorkspaceMounts(map[string]string{"../etc": d, "a/b": d, "": d, "..": d, ".hidden": d}, d, "/workspace", &warn)
	if got != nil {
		t.Errorf("accepted: %v", got)
	}
}

func TestALiveSourceThatIsAFileIsSkippedWithAWarning(t *testing.T) {
	d := t.TempDir()
	f := filepath.Join(d, "afile")
	write(t, f, "x")
	var warn strings.Builder
	if got := LiveWorkspaceMounts(map[string]string{"f": f}, d, "/workspace", &warn); got != nil || !strings.Contains(warn.String(), "f") {
		t.Errorf("got %v warn %q", got, warn.String())
	}
}

func TestExpandHomeOnlyExpandsALeadingTilde(t *testing.T) {
	for in, want := range map[string]string{"~": "/h", "~/a": "/h/a", "/x/~/y": "/x/~/y", "~other/a": "~other/a", "/abs": "/abs"} {
		if got := ExpandHome(in, "/h"); got != want {
			t.Errorf("ExpandHome(%q) = %q, want %q", in, got, want)
		}
	}
}
