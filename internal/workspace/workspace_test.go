package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/config"
)

type copies struct{ cloned, merged [][2]string }

func newStore(t *testing.T) (*Store, *copies, string) {
	t.Helper()
	base := t.TempDir()
	c := &copies{}
	s := &Store{
		Dir: filepath.Join(base, "cfg"), Root: filepath.Join(base, "ws"), Home: filepath.Join(base, "home"),
		Mount: "/workspace",
		Clone: func(src, dst string) error {
			c.cloned = append(c.cloned, [2]string{src, dst})
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dst, "copied"), []byte("x"), 0o644) // a real clone has files
		},
		Merge: func(src, dst string) error { c.merged = append(c.merged, [2]string{src, dst}); return nil },
	}
	os.MkdirAll(s.Dir, 0o755)
	os.MkdirAll(s.Root, 0o755)
	os.MkdirAll(s.Home, 0o755)
	return s, c, base
}

func src(t *testing.T, base, name string) string {
	t.Helper()
	d := filepath.Join(base, "src", name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(d)
	return real
}

func TestAddingAgainRefreshesTheCopyInsteadOfReplacingIt(t *testing.T) {
	s, c, base := newStore(t)
	d := src(t, base, "p")
	s.Add(d, "", false)
	r, err := s.Add(d, "", false)
	if err != nil || r.Action != Refreshed || len(c.merged) != 1 || c.merged[0] != [2]string{d, filepath.Join(s.Root, "p")} || len(c.cloned) != 1 {
		t.Errorf("got %+v %v merged %v", r, err, c.merged)
	}
}

func TestAddingALiveWorkspaceRecordsTheRealSourceInTheConfigAndCopiesNothing(t *testing.T) {
	s, c, base := newStore(t)
	d := src(t, base, "app")
	r, err := s.Add(d, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.cloned) != 0 || r.Action != Added || r.Entry.Kind != Live || r.Entry.Source != d || r.Entry.Host != d {
		t.Errorf("got %+v cloned %v", r, c.cloned)
	}
	w, _ := config.LoadWorkspaces(s.Dir)
	if w.Live["app"] != d {
		t.Errorf("config: %+v", w.Live)
	}
}

func TestAddingTheSameLiveWorkspaceAgainChangesNothing(t *testing.T) {
	s, _, base := newStore(t)
	d := src(t, base, "app")
	s.Add(d, "", true)
	r, err := s.Add(d, "", true)
	if err != nil || r.Action != Unchanged {
		t.Errorf("got %+v %v", r, err)
	}
}

func TestANameThatAlreadyMeansSomethingElseIsRefused(t *testing.T) {
	s, _, base := newStore(t)
	a, b := src(t, base, "a"), src(t, base, "b")
	s.Add(a, "x", true)
	for name, call := range map[string]func() error{
		"live from another source": func() error { _, err := s.Add(b, "x", true); return err },
		"local over live":          func() error { _, err := s.Add(b, "x", false); return err },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), `"x"`) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	s.Add(b, "y", false)
	if _, err := s.Add(a, "y", true); err == nil {
		t.Error("live over an existing local workspace accepted")
	}
}

func TestALiveWorkspaceMayTakeAnEmptyDirectoryButNotAFullOne(t *testing.T) {
	s, _, base := newStore(t)
	os.MkdirAll(filepath.Join(s.Root, "empty"), 0o755)
	if _, err := s.Add(src(t, base, "e"), "empty", true); err != nil {
		t.Errorf("an empty directory (a leftover mount point) was refused: %v", err)
	}
	os.MkdirAll(filepath.Join(s.Root, "full"), 0o755)
	os.WriteFile(filepath.Join(s.Root, "full", "f"), []byte("x"), 0o644)
	if _, err := s.Add(src(t, base, "f"), "full", true); err == nil {
		t.Error("a live mount would hide the files in the directory")
	}
}

func TestBadInputIsRefusedBeforeAnythingChanges(t *testing.T) {
	s, c, base := newStore(t)
	file := filepath.Join(base, "afile")
	os.WriteFile(file, []byte("x"), 0o644)
	d := src(t, base, "ok")
	for name, call := range map[string]func() error{
		"missing source": func() error { _, err := s.Add(filepath.Join(base, "none"), "", false); return err },
		"file source":    func() error { _, err := s.Add(file, "", true); return err },
		"relative name":  func() error { _, err := s.Add(d, "../x", false); return err },
		"slash name":     func() error { _, err := s.Add(d, "a/b", true); return err },
		"dot name":       func() error { _, err := s.Add(d, ".hidden", false); return err },
		"the filesystem": func() error { _, err := s.Add("/", "root", true); return err },
		"the workspace":  func() error { _, err := s.Add(s.Root, "self", false); return err },
		"inside it":      func() error { _, err := s.Add(filepath.Join(s.Root, "sub"), "sub", true); return err },
		"containing it":  func() error { _, err := s.Add(filepath.Dir(s.Root), "up", true); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if len(c.cloned) != 0 {
		t.Errorf("something was cloned: %v", c.cloned)
	}
	if w, _ := config.LoadWorkspaces(s.Dir); len(w.Live) != 0 {
		t.Errorf("config changed: %v", w.Live)
	}
}

func TestASourceInsideTheWorkspaceIsRefusedForTheRightReason(t *testing.T) {
	s, _, _ := newStore(t)
	inside := filepath.Join(s.Root, "sub")
	os.MkdirAll(inside, 0o755) // it exists, so only the overlap check can refuse it
	for _, live := range []bool{true, false} {
		if _, err := s.Add(inside, "sub", live); err == nil || !strings.Contains(err.Error(), "overlaps") {
			t.Errorf("live=%v: got %v", live, err)
		}
	}
}

func TestADirectoryWhoseNameStartsWithTwoDotsIsNotOutsideTheWorkspace(t *testing.T) {
	if !within("/a/ws", "/a/ws/..odd") || !within("/a/ws", "/a/ws") {
		t.Error("a child named ..odd was treated as outside its parent")
	}
	if within("/a/ws", "/a/ws2") || within("/a/ws", "/a") || within("/a/ws", "/b") {
		t.Error("a sibling or parent was treated as inside")
	}
}

func TestACloneThatFailsLeavesNoHalfCopy(t *testing.T) {
	s, _, base := newStore(t)
	s.Clone = func(src, dst string) error { os.MkdirAll(dst, 0o755); return errors.New("disk full") }
	d := src(t, base, "p")
	if _, err := s.Add(d, "", false); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "p")); err == nil {
		t.Error("a partial copy was left behind")
	}
}

func TestAFailedRefreshKeepsTheExistingCopy(t *testing.T) {
	s, _, base := newStore(t)
	d := src(t, base, "p")
	s.Add(d, "", false)
	s.Merge = func(string, string) error { return errors.New("rsync died") }
	if _, err := s.Add(d, "", false); err == nil {
		t.Fatal("accepted")
	}
	if _, err := os.Stat(filepath.Join(s.Root, "p")); err != nil {
		t.Error("the copy was deleted by a failed refresh")
	}
}

func TestListShowsLocalAndLiveWorkspacesInNameOrder(t *testing.T) {
	s, _, base := newStore(t)
	s.Add(src(t, base, "zeta"), "", false)
	s.Add(src(t, base, "alpha"), "", true)
	os.MkdirAll(filepath.Join(s.Root, "made-by-agent"), 0o755)
	os.MkdirAll(filepath.Join(s.Root, ".cache"), 0o755)
	os.WriteFile(filepath.Join(s.Root, "afile"), []byte("x"), 0o644)
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range got {
		names = append(names, e.Name+":"+string(e.Kind))
	}
	if strings.Join(names, ",") != "alpha:live,made-by-agent:local,zeta:local" {
		t.Errorf("got %v", names)
	}
}

func TestALiveMountPointLeftOnTheHostIsNotListedAsLocal(t *testing.T) {
	s, _, base := newStore(t)
	s.Add(src(t, base, "app"), "", true)
	os.MkdirAll(filepath.Join(s.Root, "app"), 0o755) // docker makes the mount point
	got, _ := s.List()
	if len(got) != 1 || got[0].Kind != Live {
		t.Errorf("got %+v", got)
	}
}

func TestRemovingALocalWorkspaceDeletesTheCopyAndNeverTheSource(t *testing.T) {
	s, _, base := newStore(t)
	d := src(t, base, "p")
	os.WriteFile(filepath.Join(d, "keep"), []byte("x"), 0o644)
	s.Add(d, "", false)
	os.WriteFile(filepath.Join(s.Root, "p", "agent-edit"), []byte("x"), 0o644)
	e, err := s.Remove("p")
	if err != nil || e.Kind != Local {
		t.Fatalf("got %+v %v", e, err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "p")); err == nil {
		t.Error("the copy is still there")
	}
	if _, err := os.Stat(filepath.Join(d, "keep")); err != nil {
		t.Error("the source was touched")
	}
}

func TestRemovingALiveWorkspaceUnregistersItAndLeavesTheSourceAlone(t *testing.T) {
	s, _, base := newStore(t)
	d := src(t, base, "app")
	os.WriteFile(filepath.Join(d, "precious"), []byte("x"), 0o644)
	s.Add(d, "", true)
	os.MkdirAll(filepath.Join(s.Root, "app"), 0o755) // the empty mount point
	e, err := s.Remove("app")
	if err != nil || e.Kind != Live {
		t.Fatalf("got %+v %v", e, err)
	}
	if w, _ := config.LoadWorkspaces(s.Dir); len(w.Live) != 0 {
		t.Errorf("still registered: %v", w.Live)
	}
	if _, err := os.Stat(filepath.Join(d, "precious")); err != nil {
		t.Error("the live source was touched")
	}
	if _, err := os.Stat(filepath.Join(s.Root, "app")); err == nil {
		t.Error("the empty mount point was left behind")
	}
}

func TestRemovingALiveWorkspaceNeverDeletesAFullDirectoryAtItsMountPoint(t *testing.T) {
	s, _, base := newStore(t)
	s.Add(src(t, base, "app"), "", true)
	os.MkdirAll(filepath.Join(s.Root, "app"), 0o755)
	os.WriteFile(filepath.Join(s.Root, "app", "f"), []byte("x"), 0o644)
	if _, err := s.Remove("app"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "app", "f")); err != nil {
		t.Error("files under the mount point were deleted")
	}
}

func TestRemovingAnUnknownOrUnsafeNameFailsAndDeletesNothing(t *testing.T) {
	s, _, base := newStore(t)
	s.Add(src(t, base, "p"), "", false)
	victim := filepath.Join(base, "victim")
	os.MkdirAll(victim, 0o755)
	os.WriteFile(filepath.Join(s.Root, "plain-file"), []byte("x"), 0o644)
	for _, n := range []string{"nope", "", "..", "../victim", "a/b", ".", "ws", "plain-file"} {
		if _, err := s.Remove(n); err == nil {
			t.Errorf("Remove(%q) accepted", n)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Error("a directory outside the workspace was deleted")
	}
	if _, err := os.Stat(filepath.Join(s.Root, "plain-file")); err != nil {
		t.Error("a plain file in the workspace was deleted as if it were a project")
	}
	if _, err := os.Stat(filepath.Join(s.Root, "p")); err != nil {
		t.Error("an unrelated workspace was deleted")
	}
}

func TestRemovingALocalNameThatIsASymlinkDeletesOnlyTheLink(t *testing.T) {
	s, _, base := newStore(t)
	target := filepath.Join(base, "target")
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(target, "f"), []byte("x"), 0o644)
	os.Symlink(target, filepath.Join(s.Root, "link"))
	if _, err := s.Remove("link"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "f")); err != nil {
		t.Error("the link's target was deleted")
	}
}

func TestPathGivesTheHostDirectory(t *testing.T) {
	s, _, base := newStore(t)
	d := src(t, base, "app")
	s.Add(d, "", true)
	s.Add(src(t, base, "p"), "", false)
	if got, err := s.Path("app"); err != nil || got != d {
		t.Errorf("live: %q %v", got, err)
	}
	if got, err := s.Path("p"); err != nil || got != filepath.Join(s.Root, "p") {
		t.Errorf("local: %q %v", got, err)
	}
	if got, err := s.Path(""); err != nil || got != s.Root {
		t.Errorf("root: %q %v", got, err)
	}
	if _, err := s.Path("nope"); err == nil {
		t.Error("an unknown name has a path")
	}
	if _, err := s.Path("../x"); err == nil {
		t.Error("an unsafe name has a path")
	}
}
