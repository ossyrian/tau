package build

import (
	"strconv"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/profile"
)

func prof(t *testing.T, name string) profile.Profile {
	t.Helper()
	p, err := profile.Set{Root: "/r"}.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func plansOf(t *testing.T, names ...string) []ProfilePlan {
	var out []ProfilePlan
	for _, n := range names {
		out = append(out, ProfilePlan{Profile: prof(t, n), Entries: []config.Entry{
			entry("tool-"+n, config.Package{Script: []string{"echo " + n}, As: config.AsUser}),
			entry("sys-"+n, config.Package{Script: []string{"echo sys " + n}}),
		}})
	}
	return out
}

func TestANamedProfileGetsItsUserCreatedWithAPrivateHome(t *testing.T) {
	out, err := RenderPlans(plansOf(t, "default", "work"))
	if err != nil {
		t.Fatal(err)
	}
	w := prof(t, "work")
	for _, want := range []string{
		"useradd -m -u " + itoa(w.UID) + " -d /home/p-work -s /bin/zsh -G tau-clients p-work",
		"chmod 700 /home/p-work",
		"COPY --chown=" + itoa(w.UID) + ":" + itoa(w.UID) + " zshrc.base /home/p-work/.zshrc",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "useradd -m -u 1000") {
		t.Error("the default user is created in the Dockerfile, not here")
	}
}

func TestUserPackagesRunAsTheirProfilesUser(t *testing.T) {
	out, _ := RenderPlans(plansOf(t, "default", "work"))
	i := strings.Index(out, "# package: tool-work")
	if i < 0 {
		t.Fatal("no work package")
	}
	if !strings.HasPrefix(out[i:], "# package: tool-work\nUSER p-work\n") {
		t.Errorf("got %q", out[i:i+60])
	}
	j := strings.Index(out, "# package: tool-default")
	if !strings.HasPrefix(out[j:], "# package: tool-default\nUSER pi\n") {
		t.Errorf("got %q", out[j:j+60])
	}
}

func TestMarkersAreKeptPerProfileAndOwnedByIt(t *testing.T) {
	out, _ := RenderPlans(plansOf(t, "default", "work"))
	for _, want := range []string{
		"install -d -o p-work -g p-work /etc/tau/installed/p-work",
		"> /etc/tau/installed/p-work/tool-work\n",
		"> /etc/tau/installed/p-work/sys-work\n",
		"> /etc/tau/installed/tool-default\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestPlansHashSeparatesWhichProfileOwnsAPackage(t *testing.T) {
	e := entry("x", config.Package{Script: []string{"s"}})
	a := []ProfilePlan{{Profile: prof(t, "default"), Entries: []config.Entry{e}}, {Profile: prof(t, "work")}}
	b := []ProfilePlan{{Profile: prof(t, "default")}, {Profile: prof(t, "work"), Entries: []config.Entry{e}}}
	if PlansHash(a) == PlansHash(b) {
		t.Error("moving a package between profiles kept the hash")
	}
}

func TestAnErrorNamesTheProfile(t *testing.T) {
	bad := []ProfilePlan{{Profile: prof(t, "work"), Entries: []config.Entry{entry("bad", config.Package{})}}}
	_, err := RenderPlans(bad)
	if err == nil || !strings.Contains(err.Error(), `profile "work"`) || !strings.Contains(err.Error(), `"bad"`) {
		t.Errorf("got %v", err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestEveryProfileUserMayReachTheProxySockets(t *testing.T) {
	out, _ := RenderPlans(plansOf(t, "default", "work", "play"))
	if strings.Count(out, "-G tau-clients") != 2 {
		t.Errorf("the named profiles must join tau-clients:\n%s", out)
	}
}
