package skills

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/config"
)

type env struct {
	home    string
	mounts  map[string]string // container dest -> host source
	running bool
}

func newEnv(t *testing.T) *env {
	t.Helper()
	h, _ := filepath.EvalSymlinks(t.TempDir())
	return &env{home: h, running: true, mounts: map[string]string{}}
}

func (e *env) store() *Store {
	return &Store{
		Dir: e.home, Home: e.home,
		Mounted: func() (map[string]string, bool, error) { return e.mounts, e.running, nil },
	}
}

// skill makes a skill directory at <home>/<parent>/<name>.
func (e *env) skill(t *testing.T, parent, name string, withFile bool) string {
	t.Helper()
	d := filepath.Join(e.home, parent, name)
	os.MkdirAll(d, 0o755)
	if withFile {
		os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644)
	}
	return d
}

func (e *env) cfg(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.home, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *env) saved(t *testing.T) config.Skills {
	t.Helper()
	s, err := config.LoadSkills(e.home)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func names(l []Skill) []string {
	var out []string
	for _, k := range l {
		out = append(out, k.Name)
	}
	return out
}

func TestListStates(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.home, "s")
	synced, pending := e.skill(t, "s", "synced", true), e.skill(t, "s", "pending", true)
	e.skill(t, "s", "off", true)
	stale := e.skill(t, "elsewhere", "stale", true)
	e.cfg(t, "skills:\n  paths: ["+dir+"]\n  active: [synced, pending, ghost]\n")
	e.mounts[Dest+"synced"] = synced
	e.mounts[Dest+"stale"] = stale
	got, err := e.store().List()
	if err != nil {
		t.Fatal(err)
	}
	want := []Skill{
		{"off", filepath.Join(dir, "off"), false, Inactive},
		{"pending", pending, true, Pending},
		{"synced", synced, true, Synced},
		{"ghost", "", true, Missing},
		{"stale", stale, false, Stale},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestListActiveSkillsReadConfiguredWhenTheContainerIsStopped(t *testing.T) {
	e := newEnv(t)
	e.skill(t, "s", "a", true)
	e.skill(t, "s", "b", true)
	e.running = false
	e.cfg(t, "skills:\n  paths: ["+filepath.Join(e.home, "s")+"]\n  active: [a]\n")
	e.mounts[Dest+"zzz"] = "/old" // a stopped container has nothing to compare against
	got, _ := e.store().List()
	if got[0].State != Configured || got[1].State != Inactive {
		t.Errorf("got %+v", got)
	}
}

func TestListActiveByPathOutsideEverySearchPath(t *testing.T) {
	e := newEnv(t)
	d := e.skill(t, "elsewhere", "solo", true)
	e.cfg(t, "skills:\n  paths: []\n  active: ["+d+"]\n")
	got, _ := e.store().List()
	if len(got) != 1 || got[0].Name != "solo" || !got[0].Active || got[0].State != Pending {
		t.Errorf("got %+v", got)
	}
}

func TestListFirstPathWinsOnANameClash(t *testing.T) {
	e := newEnv(t)
	first, _ := e.skill(t, "one", "dup", true), e.skill(t, "two", "dup", true)
	e.cfg(t, "skills:\n  paths: ["+filepath.Join(e.home, "one")+", "+filepath.Join(e.home, "two")+"]\n")
	got, _ := e.store().List()
	if len(got) != 1 || got[0].Path != first {
		t.Errorf("got %+v", got)
	}
}

func TestListComparesMountsByRealPath(t *testing.T) {
	e := newEnv(t)
	target := e.skill(t, "real", "linked", true)
	os.MkdirAll(filepath.Join(e.home, "s"), 0o755)
	os.Symlink(target, filepath.Join(e.home, "s", "linked"))
	e.cfg(t, "skills:\n  paths: ["+filepath.Join(e.home, "s")+"]\n  active: [linked]\n")
	e.mounts[Dest+"linked"] = target
	got, _ := e.store().List()
	if len(got) != 1 || got[0].State != Synced {
		t.Errorf("got %+v", got)
	}
}

func TestDriftCountsPendingAndStaleOnly(t *testing.T) {
	e := newEnv(t)
	s := filepath.Join(e.home, "s")
	e.skill(t, "s", "pending", true)
	e.skill(t, "s", "synced", true)
	e.skill(t, "s", "off", true)
	e.cfg(t, "skills:\n  paths: ["+s+"]\n  active: [pending, synced, ghost]\n")
	e.mounts[Dest+"synced"] = filepath.Join(s, "synced")
	e.mounts[Dest+"old"] = "/x"
	if n, _ := e.store().Drift(); n != 2 {
		t.Errorf("drift %d, want 2 (pending + stale)", n)
	}
}

func TestListReportsAConfigError(t *testing.T) {
	e := newEnv(t)
	e.cfg(t, "skills: [oops\n")
	if _, err := e.store().List(); err == nil || !strings.Contains(err.Error(), "config.yaml") {
		t.Errorf("err %v", err)
	}
}

func TestActivateByNameAndByPath(t *testing.T) {
	e := newEnv(t)
	e.skill(t, "s", "alpha", true)
	other := e.skill(t, "elsewhere", "beta", true)
	e.cfg(t, "skills:\n  paths: ["+filepath.Join(e.home, "s")+"]\n")
	st := e.store()
	if n, err := st.Activate("alpha"); err != nil || n != "alpha" {
		t.Fatalf("%q %v", n, err)
	}
	if n, err := st.Activate(other); err != nil || n != "beta" {
		t.Fatalf("%q %v", n, err)
	}
	if got := e.saved(t).Active; !reflect.DeepEqual(got, []string{"alpha", other}) {
		t.Errorf("saved %v", got)
	}
}

func TestActivateByPathInsideASearchPathStoresTheName(t *testing.T) {
	e := newEnv(t)
	d := e.skill(t, "s", "alpha", true)
	e.cfg(t, "skills:\n  paths: ["+filepath.Join(e.home, "s")+"]\n")
	if _, err := e.store().Activate(d); err != nil {
		t.Fatal(err)
	}
	if got := e.saved(t).Active; !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Errorf("saved %v", got)
	}
}

func TestActivateRefusesWhatItCannotLoad(t *testing.T) {
	e := newEnv(t)
	e.skill(t, "s", "alpha", true)
	nofile := e.skill(t, "other", "nofile", false)
	e.cfg(t, "skills:\n  paths: ["+filepath.Join(e.home, "s")+"]\n")
	for in, want := range map[string]string{
		"nope":                          "no skill named",
		nofile:                          "no SKILL.md",
		filepath.Join(e.home, "absent"): "not a directory",
		"/bad:path":                     "may not contain",
	} {
		_, err := e.store().Activate(in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err %v, want %q", in, err, want)
		}
	}
	if got := e.saved(t).Active; len(got) != 0 {
		t.Errorf("saved %v", got)
	}
}

func TestActivateRefusesDuplicatesAndNameClashes(t *testing.T) {
	e := newEnv(t)
	e.skill(t, "s", "alpha", true)
	clash := e.skill(t, "other", "alpha", true)
	e.cfg(t, "skills:\n  paths: ["+filepath.Join(e.home, "s")+"]\n  active: [alpha]\n")
	if _, err := e.store().Activate("alpha"); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Errorf("duplicate: %v", err)
	}
	if _, err := e.store().Activate(clash); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Errorf("clash: %v", err)
	}
	if got := e.saved(t).Active; len(got) != 1 {
		t.Errorf("saved %v", got)
	}
}

func TestDeactivateByNameAndByPath(t *testing.T) {
	e := newEnv(t)
	e.skill(t, "s", "alpha", true)
	other := e.skill(t, "elsewhere", "beta", true)
	e.skill(t, "s", "gamma", true)
	e.cfg(t, "skills:\n  paths: ["+filepath.Join(e.home, "s")+"]\n  active: [alpha, "+other+", gamma]\n")
	st := e.store()
	if n, err := st.Deactivate("alpha"); err != nil || n != "alpha" {
		t.Fatalf("%q %v", n, err)
	}
	if n, err := st.Deactivate(other); err != nil || n != "beta" {
		t.Fatalf("%q %v", n, err)
	}
	if got := e.saved(t).Active; !reflect.DeepEqual(got, []string{"gamma"}) {
		t.Errorf("saved %v", got)
	}
	// A path to a skill that was activated by name works too.
	if _, err := st.Deactivate(filepath.Join(e.home, "s", "gamma")); err != nil {
		t.Fatal(err)
	}
	if got := e.saved(t).Active; len(got) != 0 {
		t.Errorf("saved %v", got)
	}
}

func TestDeactivateDropsAMissingEntryAndNeverTouchesFiles(t *testing.T) {
	e := newEnv(t)
	d := e.skill(t, "s", "alpha", true)
	e.cfg(t, "skills:\n  paths: [/nowhere]\n  active: [ghost, "+d+"]\n")
	if _, err := e.store().Deactivate("ghost"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store().Deactivate("alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d, "SKILL.md")); err != nil {
		t.Error("the skill directory was touched")
	}
}

func TestDeactivateUnknown(t *testing.T) {
	e := newEnv(t)
	e.cfg(t, "skills:\n  active: [a]\n")
	if _, err := e.store().Deactivate("b"); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Errorf("err %v", err)
	}
	if got := e.saved(t).Active; len(got) != 1 {
		t.Errorf("saved %v", got)
	}
}

func TestPathsAddRemoveRename(t *testing.T) {
	e := newEnv(t)
	e.skill(t, "one", "a", true)
	e.skill(t, "two", "b", true)
	e.skill(t, "two", "c", true)
	one, two, three := filepath.Join(e.home, "one"), filepath.Join(e.home, "two"), filepath.Join(e.home, "three")
	os.MkdirAll(three, 0o755)
	e.cfg(t, "skills:\n  paths: ["+one+"]\n")
	st := e.store()

	if err := st.AddPath(two); err != nil {
		t.Fatal(err)
	}
	info, _ := st.Paths()
	want := []PathInfo{{one, true, 1}, {two, true, 2}}
	if !reflect.DeepEqual(info, want) {
		t.Errorf("paths %+v", info)
	}

	if err := st.RenamePath(one, three); err != nil {
		t.Fatal(err)
	}
	if got := e.saved(t).Paths; !reflect.DeepEqual(got, []string{three, two}) {
		t.Errorf("rename should keep the place: %v", got)
	}

	if err := st.RemovePath(three); err != nil {
		t.Fatal(err)
	}
	if got := e.saved(t).Paths; !reflect.DeepEqual(got, []string{two}) {
		t.Errorf("remove: %v", got)
	}
	if _, err := os.Stat(three); err != nil {
		t.Error("remove deleted the directory")
	}
}

func TestPathsMatchTildeAndTrailingSlashSpellings(t *testing.T) {
	e := newEnv(t)
	e.skill(t, "mine", "x", true)
	d := filepath.Join(e.home, "mine")
	e.cfg(t, "skills:\n  paths: [~/mine]\n")
	st := e.store()
	if err := st.AddPath(d + "/"); err == nil {
		t.Error("added the same directory twice")
	}
	if err := st.RemovePath(d); err != nil {
		t.Errorf("remove by another spelling: %v", err)
	}
}

func TestPathsErrors(t *testing.T) {
	e := newEnv(t)
	d := e.skill(t, "s", "x", true)
	file := filepath.Join(e.home, "afile")
	os.WriteFile(file, []byte("x"), 0o644)
	e.cfg(t, "skills:\n  paths: ["+d+"]\n")
	st := e.store()
	for name, err := range map[string]error{
		"relative":      st.AddPath("rel/dir"),
		"empty":         st.AddPath(""),
		"missing":       st.AddPath(filepath.Join(e.home, "absent")),
		"file":          st.AddPath(file),
		"colon":         st.AddPath("/a:b"),
		"duplicate":     st.AddPath(d),
		"rm unknown":    st.RemovePath(filepath.Join(e.home, "nope")),
		"rename from":   st.RenamePath(filepath.Join(e.home, "nope"), d),
		"rename to bad": st.RenamePath(d, filepath.Join(e.home, "absent")),
	} {
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if got := e.saved(t).Paths; !reflect.DeepEqual(got, []string{d}) {
		t.Errorf("a failed edit changed the paths: %v", got)
	}
}

func TestRenameToAnotherSpellingOfItselfIsAllowed(t *testing.T) {
	// Editing a path and keeping it the same must not be reported as a duplicate.
	e := newEnv(t)
	d := e.skill(t, "s", "x", true)
	e.cfg(t, "skills:\n  paths: ["+d+"]\n")
	if err := e.store().RenamePath(d, d+"/"); err != nil {
		t.Errorf("err %v", err)
	}
}

func TestRemovingAPathMakesItsNameOnlyActivesMissing(t *testing.T) {
	e := newEnv(t)
	e.skill(t, "s", "a", true)
	e.cfg(t, "skills:\n  paths: ["+filepath.Join(e.home, "s")+"]\n  active: [a]\n")
	e.store().RemovePath(filepath.Join(e.home, "s"))
	got, _ := e.store().List()
	if len(got) != 1 || got[0].State != Missing {
		t.Errorf("got %+v", got)
	}
}

func TestMountsHoldOnlyActiveSkillsThatExist(t *testing.T) {
	e := newEnv(t)
	a := e.skill(t, "s", "a", true)
	e.skill(t, "s", "off", true)
	byPath := e.skill(t, "elsewhere", "p", true)
	e.cfg(t, "skills:\n  paths: ["+filepath.Join(e.home, "s")+"]\n  active: [a, ghost, "+byPath+", a]\n")
	m, warn, err := e.store().Mounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m[0].Name != "a" || m[0].Path != a || m[1].Name != "p" || m[1].Path != byPath {
		t.Errorf("mounts %+v", m)
	}
	if len(warn) != 2 || !strings.Contains(warn[0], "ghost") || !strings.Contains(warn[1], `"a"`) {
		t.Errorf("warnings %q", warn)
	}
}

func TestMountsRefuseUnsafeNames(t *testing.T) {
	e := newEnv(t)
	d := e.skill(t, "s", "bad name", true)
	e.cfg(t, "skills:\n  paths: []\n  active: ['"+d+"']\n")
	if m, warn, _ := e.store().Mounts(); len(m) != 0 || len(warn) != 1 {
		t.Errorf("mounts %v warnings %v", m, warn)
	}
}

func TestFilterMatchesNameOrPathFuzzily(t *testing.T) {
	l := []Skill{
		{Name: "python-testing", Path: "/h/house/02-python-testing"},
		{Name: "voice", Path: "/h/agents/voice"},
		{Name: "prune", Path: "/h/agents/prune"},
	}
	for query, want := range map[string][]string{
		"":         {"python-testing", "voice", "prune"},
		"   ":      {"python-testing", "voice", "prune"},
		"voi":      {"voice"},
		"pytst":    {"python-testing"},
		"house":    {"python-testing"},
		"agents":   {"voice", "prune"},
		"zzzz":     nil,
		"AGENTS/V": {"voice"},
	} {
		if got := names(Filter(l, query)); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v want %v", query, got, want)
		}
	}
}

func TestListSkipsDirectoriesWithUnsafeNames(t *testing.T) {
	// The name becomes a docker mount destination, so it must be one safe path part.
	e := newEnv(t)
	e.skill(t, "s", "good", true)
	e.skill(t, "s", "bad name", true)
	e.skill(t, "s", "-dash", true)
	e.skill(t, "s", "semi;colon", true)
	e.cfg(t, "skills:\n  paths: ["+filepath.Join(e.home, "s")+"]\n")
	got, _ := e.store().List()
	if !reflect.DeepEqual(names(got), []string{"good"}) {
		t.Errorf("got %v", names(got))
	}
}

func TestActivateRefusesTheSameDirectoryUnderAnotherName(t *testing.T) {
	e := newEnv(t)
	d := e.skill(t, "s", "alpha", true)
	alias := filepath.Join(e.home, "elsewhere", "alias")
	os.MkdirAll(filepath.Dir(alias), 0o755)
	os.Symlink(d, alias)
	e.cfg(t, "skills:\n  paths: ["+filepath.Join(e.home, "s")+"]\n  active: [alpha]\n")
	if _, err := e.store().Activate(alias); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Errorf("err %v", err)
	}
	if got := e.saved(t).Active; len(got) != 1 {
		t.Errorf("saved %v", got)
	}
}

func TestFilterRanksNameMatchesBeforePathOnlyMatches(t *testing.T) {
	// "ga" is scattered through a long path, but only gamma is named for it.
	l := []Skill{
		{Name: "triage-linear-ticket", Path: "/Users/x/.agents/skills/triage-linear-ticket"},
		{Name: "alpha", Path: "/tmp/Cgvc/pool/alpha"},
		{Name: "gamma", Path: "/tmp/pool/gamma"},
	}
	got := names(Filter(l, "ga"))
	if len(got) == 0 || got[0] != "gamma" {
		t.Errorf("the skill named for the query should come first: %v", got)
	}
}

func TestListShowsAnActiveByPathSkillAtTheSpelledPath(t *testing.T) {
	e := newEnv(t)
	target := e.skill(t, "real", "solo", true)
	link := filepath.Join(e.home, "elsewhere", "solo")
	os.MkdirAll(filepath.Dir(link), 0o755)
	os.Symlink(target, link)
	e.cfg(t, "skills:\n  paths: []\n  active: ["+link+"]\n")
	got, _ := e.store().List()
	if len(got) != 1 || got[0].Path != link {
		t.Errorf("got %+v", got)
	}
}

func TestAProfilesSkillsAreComparedAgainstItsOwnMountDirectory(t *testing.T) {
	e := newEnv(t)
	skill := e.skill(t, "s", "voice", true)
	e.cfg(t, "skills:\n  paths: ["+filepath.Join(e.home, "s")+"]\n  active: [voice]\n")
	s := e.store()
	s.Dest = DestFor("/home/p-work")
	e.mounts[DestFor("/home/pi")+"voice"] = skill // another profile's mount must not count
	got, err := s.List()
	if err != nil || len(got) != 1 || got[0].State != Pending {
		t.Fatalf("got %+v %v, want pending", got, err)
	}
	e.mounts[DestFor("/home/p-work")+"voice"] = skill
	if got, _ = s.List(); got[0].State != Synced {
		t.Errorf("got %q, want synced", got[0].State)
	}
	if n, _ := s.Drift(); n != 0 {
		t.Errorf("drift %d with everything mounted", n)
	}
}
