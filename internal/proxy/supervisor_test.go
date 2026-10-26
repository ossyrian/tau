package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// fakeDocker records `docker exec` calls and their stdin.
type fakeDocker struct {
	mu      sync.Mutex
	calls   [][]string
	stdin   []string
	fail    map[string]error // by a substring of the joined arguments
	running map[string]bool  // proxies whose server answers `tau-proxy up`
	changed string           // printed by the user-setup script: proxies whose groups changed
}

func (f *fakeDocker) Exec(stdin io.Reader, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, args)
	body := ""
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		body = string(b)
	}
	f.stdin = append(f.stdin, body)
	joined := strings.Join(args, " ")
	for k, err := range f.fail {
		if strings.Contains(joined, k) {
			return "", err
		}
	}
	if i := strings.Index(joined, "tau-proxy up "); i >= 0 {
		name := strings.Fields(joined[i+len("tau-proxy up "):])[0]
		if !f.running[name] {
			return "", errors.New("no server")
		}
	}
	if strings.Contains(body, "useradd") {
		return f.changed, nil
	}
	return "", nil
}

func supervisor(t *testing.T, f *fakeDocker) *Supervisor {
	return &Supervisor{
		D: f, Container: "tau",
		Host: Host{Proxies: proxies(), Members: members(t), Secrets: fakeSecrets(t, map[string][]Var{"aws-dev": {{Key: "K", Value: "v"}}})},
	}
}

// callsAs returns the recorded calls made as user, as one string each.
func callsAs(f *fakeDocker, user string) []string {
	var out []string
	for _, c := range f.calls {
		for i := 0; i+1 < len(c); i++ {
			if c[i] == "-u" && c[i+1] == user {
				out = append(out, strings.Join(c, " "))
				break
			}
		}
	}
	return out
}

// docFor returns the document written to a proxy's pipe or sent to its reload.
func docFor(t *testing.T, f *fakeDocker, name string) (Bootstrap, bool) {
	t.Helper()
	for i, c := range f.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "-u "+UserFor(name)+" ") && (strings.Contains(j, "cat >") || strings.Contains(j, "tau-proxy reload")) {
			var b Bootstrap
			if err := json.Unmarshal([]byte(f.stdin[i]), &b); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			return b, true
		}
	}
	return Bootstrap{}, false
}

func launched(f *fakeDocker, name string) bool {
	for _, c := range callsAs(f, UserFor(name)) {
		if strings.HasPrefix(c, "exec -d ") && strings.Contains(c, "tau-proxy serve") {
			return true
		}
	}
	return false
}

func reloaded(f *fakeDocker, name string) bool {
	for _, c := range callsAs(f, UserFor(name)) {
		if strings.Contains(c, "tau-proxy reload") {
			return true
		}
	}
	return false
}

func TestStartLaunchesOneServerPerUsedProxyEachWithItsOwnDocument(t *testing.T) {
	f := &fakeDocker{}
	if err := supervisor(t, f).Start(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"aws-dev", "aws-prod", "git", "tf"} {
		fifo := DirFor(n) + "/boot"
		var made, served, wrote bool
		for _, c := range callsAs(f, UserFor(n)) {
			made = made || (strings.Contains(c, "mkfifo -m 600") && strings.HasSuffix(c, fifo))
			served = served || (strings.HasPrefix(c, "exec -d ") && strings.Contains(c, "tau-proxy serve") && strings.HasSuffix(c, n+" "+fifo))
			wrote = wrote || (strings.HasPrefix(c, "exec -i ") && strings.Contains(c, "cat >") && strings.HasSuffix(c, fifo))
		}
		if !made || !served || !wrote {
			t.Errorf("%s: made=%v served=%v wrote=%v: %v", n, made, served, wrote, f.calls)
		}
	}
	dev, _ := docFor(t, f, "aws-dev")
	if dev.Proxies["aws-dev"].Env["K"] != "v" || len(dev.Proxies) != 1 {
		t.Errorf("dev doc: %+v", dev)
	}
	if dev.Proxies["aws-dev"].Minted == nil {
		t.Error("a new server must be sent minted credentials, even if none")
	}
}

func TestUsersAreMadeBeforeAnyServerStarts(t *testing.T) {
	f := &fakeDocker{}
	supervisor(t, f).Start()
	setup := -1
	for i := range f.calls {
		if strings.Contains(f.stdin[i], "useradd") {
			setup = i
		}
	}
	if setup < 0 {
		t.Fatal("no user setup")
	}
	for i, c := range f.calls {
		if strings.Contains(strings.Join(c, " "), "tau-proxy serve") && i < setup {
			t.Errorf("a server started before its user existed: %v", c)
		}
	}
	script := f.stdin[setup]
	for _, want := range []string{"tx-aws-dev", "tx-git", "tx-tf", "-g " + PackagesGroup, "tau-proxy"} {
		if !strings.Contains(script, want) {
			t.Errorf("setup script lacks %q:\n%s", want, script)
		}
	}
	if !strings.Contains(script, itoa(UIDFor("aws-dev"))) {
		t.Errorf("the uid is not fixed:\n%s", script)
	}
}

func TestAProxyUserCanOwnItsSocketForTheClientsGroup(t *testing.T) {
	// Listen chowns the socket to tau-clients; a user may only do that for a group it is in.
	f := &fakeDocker{}
	supervisor(t, f).Start()
	var script string
	for _, s := range f.stdin {
		if strings.Contains(s, "useradd") {
			script = s
		}
	}
	if !strings.Contains(script, "-g "+PackagesGroup+" -G "+ClientsGroup) {
		t.Errorf("proxy users are not in %s:\n%s", ClientsGroup, script)
	}
}

func TestOnlyAnAgentDirectoryProxyUserJoinsTheSharedGroup(t *testing.T) {
	f := &fakeDocker{}
	supervisor(t, f).Start()
	var script string
	for _, s := range f.stdin {
		if strings.Contains(s, "useradd") {
			script = s
		}
	}
	line := func(user string) string {
		for _, l := range strings.Split(script, "\n") {
			if strings.HasPrefix(l, "ensure ") && strings.Contains(l, " '"+user+"' ") {
				return l
			}
		}
		return ""
	}
	if !strings.Contains(line("tx-git"), "shared") || strings.Contains(line("tx-aws-dev"), "shared") || strings.Contains(line("tx-tf"), "shared") {
		t.Errorf("git: %q aws-dev: %q tf: %q", line("tx-git"), line("tx-aws-dev"), line("tx-tf"))
	}
}

func TestReloadSendsADocumentToEachRunningServerWithoutMinting(t *testing.T) {
	f := &fakeDocker{running: map[string]bool{"aws-dev": true, "aws-prod": true, "git": true, "tf": true}}
	s := supervisor(t, f)
	minted := 0
	d := s.Host.Proxies.Defs["aws-dev"]
	d.EnvFrom = "mint"
	s.Host.Proxies.Defs["aws-dev"] = d
	s.Host.Secrets.Run = func(string) (string, error) { minted++; return "", nil }
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if minted != 0 {
		t.Errorf("Reload ran env_from %d times", minted)
	}
	for _, n := range []string{"aws-dev", "aws-prod", "git", "tf"} {
		if !reloaded(f, n) || launched(f, n) {
			t.Errorf("%s: reloaded=%v launched=%v", n, reloaded(f, n), launched(f, n))
		}
	}
	b, _ := docFor(t, f, "aws-dev")
	if b.Proxies["aws-dev"].Minted != nil {
		t.Error("a reload must leave Minted nil so held credentials are kept")
	}
}

func TestReloadStartsAServerThatIsNotRunningAndMintsOnlyThat(t *testing.T) {
	f := &fakeDocker{running: map[string]bool{"aws-dev": true, "git": true, "tf": true}} // aws-prod is new
	s := supervisor(t, f)
	var ran []string
	for _, n := range []string{"aws-dev", "aws-prod"} {
		d := s.Host.Proxies.Defs[n]
		d.EnvFrom = "mint-" + n
		s.Host.Proxies.Defs[n] = d
	}
	s.Host.Secrets.Run = func(c string) (string, error) { ran = append(ran, c); return "", nil }
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if !launched(f, "aws-prod") || launched(f, "aws-dev") || !reflect.DeepEqual(ran, []string{"mint-aws-prod"}) {
		t.Errorf("launched prod=%v dev=%v ran=%v", launched(f, "aws-prod"), launched(f, "aws-dev"), ran)
	}
}

func TestRefreshMintsAndReloadsOnlyTheNamedProxies(t *testing.T) {
	f := &fakeDocker{running: map[string]bool{"aws-dev": true, "aws-prod": true, "git": true, "tf": true}}
	s := supervisor(t, f)
	var ran []string
	for _, n := range []string{"aws-dev", "aws-prod"} {
		d := s.Host.Proxies.Defs[n]
		d.EnvFrom = "mint-" + n
		s.Host.Proxies.Defs[n] = d
	}
	s.Host.Secrets.Run = func(c string) (string, error) { ran = append(ran, c); return "TOKEN=new\n", nil }
	if err := s.Refresh([]string{"aws-prod"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ran, []string{"mint-aws-prod"}) {
		t.Errorf("ran %v", ran)
	}
	if !reloaded(f, "aws-prod") {
		t.Error("aws-prod not reloaded")
	}
	for _, n := range []string{"aws-dev", "git", "tf"} {
		if reloaded(f, n) || launched(f, n) {
			t.Errorf("%s was touched by a refresh of aws-prod", n)
		}
	}
	b, _ := docFor(t, f, "aws-prod")
	if b.Proxies["aws-prod"].Minted["TOKEN"] != "new" {
		t.Errorf("doc %+v", b)
	}
}

func TestRefreshWithNoNamesRefreshesEveryUsedProxy(t *testing.T) {
	f := &fakeDocker{running: map[string]bool{"aws-dev": true, "aws-prod": true, "git": true, "tf": true}}
	s := supervisor(t, f)
	if err := s.Refresh(nil); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"aws-dev", "aws-prod", "git", "tf"} {
		b, ok := docFor(t, f, n)
		if !ok || b.Proxies[n].Minted == nil {
			t.Errorf("%s: not refreshed (%v)", n, ok)
		}
	}
}

func TestRefreshOfAProxyNoProfileUsesIsAnError(t *testing.T) {
	f := &fakeDocker{}
	s := supervisor(t, f)
	s.Host.Members = s.Host.Members[:1] // default only: aws-dev and git
	err := s.Refresh([]string{"aws-prod"})
	if err == nil || !strings.Contains(err.Error(), "aws-prod") {
		t.Errorf("got %v", err)
	}
	err = s.Refresh([]string{"ghost"})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("got %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("something ran: %v", f.calls)
	}
}

func TestAFailedMintKeepsEveryRunningServerUntouched(t *testing.T) {
	f := &fakeDocker{running: map[string]bool{"aws-dev": true, "aws-prod": true, "git": true, "tf": true}}
	s := supervisor(t, f)
	d := s.Host.Proxies.Defs["aws-prod"]
	d.EnvFrom = "mint"
	s.Host.Proxies.Defs["aws-prod"] = d
	s.Host.Secrets.Run = func(string) (string, error) { return "", errors.New("sso expired") }
	err := s.Refresh([]string{"aws-prod"})
	if err == nil || !strings.Contains(err.Error(), "sso expired") || !strings.Contains(err.Error(), "aws-prod") {
		t.Fatalf("got %v", err)
	}
	for _, c := range f.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "reload") || strings.Contains(j, "serve") || strings.Contains(j, "pkill") {
			t.Errorf("a server was changed after the mint failed: %v", c)
		}
	}
}

func TestAMintFailureAtStartLaunchesNothing(t *testing.T) {
	f := &fakeDocker{}
	s := supervisor(t, f)
	d := s.Host.Proxies.Defs["aws-prod"]
	d.EnvFrom = "mint"
	s.Host.Proxies.Defs["aws-prod"] = d
	s.Host.Secrets.Run = func(string) (string, error) { return "", errors.New("sso expired") }
	err := s.Start()
	if err == nil || !strings.Contains(err.Error(), "sso expired") {
		t.Errorf("got %v", err)
	}
	for _, c := range f.calls {
		if j := strings.Join(c, " "); strings.Contains(j, "serve") || strings.Contains(j, "mkfifo") {
			t.Errorf("a server was started: %v", c)
		}
	}
}

func TestNoSecretEverAppearsInACommandLine(t *testing.T) {
	f := &fakeDocker{running: map[string]bool{"aws-dev": true}}
	s := supervisor(t, f)
	s.Host.Secrets = fakeSecrets(t, map[string][]Var{"aws-dev": {{Key: "AWS_SECRET_ACCESS_KEY", Value: "TOP-SECRET-VALUE"}}, "git": {{Key: "GH_TOKEN", Value: "ANOTHER-SECRET"}}})
	d := s.Host.Proxies.Defs["aws-prod"]
	d.EnvFrom = "mint"
	s.Host.Proxies.Defs["aws-prod"] = d
	s.Host.Secrets.Run = func(string) (string, error) { return "MINTED=MINTED-SECRET\n", nil }
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh([]string{"aws-prod"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		for _, a := range c {
			for _, bad := range []string{"TOP-SECRET", "ANOTHER-SECRET", "MINTED-SECRET"} {
				if strings.Contains(a, bad) {
					t.Errorf("secret in argv: %v", c)
				}
			}
		}
	}
}

func TestEachDocumentGoesToItsOwnUsersProcessOnly(t *testing.T) {
	f := &fakeDocker{}
	s := supervisor(t, f)
	s.Host.Secrets = fakeSecrets(t, map[string][]Var{"aws-dev": {{Key: "K", Value: "DEV-ONLY"}}, "aws-prod": {{Key: "K", Value: "PROD-ONLY"}}})
	s.Start()
	for i, c := range f.calls {
		j := strings.Join(c, " ")
		if !strings.Contains(j, "cat >") {
			continue
		}
		if strings.Contains(f.stdin[i], "DEV-ONLY") && !strings.Contains(j, "-u tx-aws-dev ") {
			t.Errorf("the dev secret went to %s", j)
		}
		if strings.Contains(f.stdin[i], "PROD-ONLY") && !strings.Contains(j, "-u tx-aws-prod ") {
			t.Errorf("the prod secret went to %s", j)
		}
	}
}

func TestAServerThatCannotStartIsReported(t *testing.T) {
	f := &fakeDocker{fail: map[string]error{"tau-proxy serve": errors.New("exec failed")}}
	err := supervisor(t, f).Start()
	if err == nil || !strings.Contains(err.Error(), "exec failed") {
		t.Errorf("got %v", err)
	}
}

func TestAProxyWhoseGroupsChangedHasItsRunningServerKilledAndStartedAgain(t *testing.T) {
	// The fake says git still answers; the group change must start it anyway.
	f := &fakeDocker{running: map[string]bool{"aws-dev": true, "aws-prod": true, "git": true, "tf": true}, changed: "git\n"}
	if err := supervisor(t, f).Reload(); err != nil {
		t.Fatal(err)
	}
	killAt, launchAt := -1, -1
	for i, c := range f.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "pkill -KILL -u tx-git") {
			killAt = i
		}
		if strings.HasPrefix(j, "exec -d ") && strings.Contains(j, "-u tx-git ") {
			launchAt = i
		}
	}
	if killAt < 0 || launchAt < killAt {
		t.Errorf("kill at %d, launch at %d: want the old server killed, then a new one launched", killAt, launchAt)
	}
	if reloaded(f, "git") {
		t.Error("git was reloaded in place; the old process keeps its old groups")
	}
	for _, n := range []string{"aws-dev", "aws-prod", "tf"} {
		if launched(f, n) {
			t.Errorf("%s was restarted though its groups did not change", n)
		}
	}
}

func TestServersOfProxiesNoLongerUsedAreStoppedAndTheirDirectoriesRemoved(t *testing.T) {
	f := &fakeDocker{}
	s := supervisor(t, f)
	s.Start()
	var script string
	for _, in := range f.stdin {
		if strings.Contains(in, "useradd") {
			script = in
		}
	}
	for _, want := range []string{SocketDir, "pkill -KILL", "rm -rf"} {
		if !strings.Contains(script, want) {
			t.Errorf("setup script lacks %q:\n%s", want, script)
		}
	}
	// The set of kept names must be passed so everything else goes.
	for _, n := range []string{"aws-dev", "aws-prod", "git", "tf"} {
		if !strings.Contains(script, "'"+n+"'") && !strings.Contains(script, " "+n+" ") && !strings.Contains(script, " "+n+"\n") {
			t.Errorf("kept name %s missing", n)
		}
	}
}

func TestShimsAreWrittenOncePerAgentCommand(t *testing.T) {
	f := &fakeDocker{}
	s := supervisor(t, f)
	if err := s.InstallShims(); err != nil {
		t.Fatal(err)
	}
	var all string
	for i := range f.calls {
		all += strings.Join(f.calls[i], " ") + "\n" + f.stdin[i] + "\n"
	}
	for _, want := range []string{"aws", "git", "terraform", ShimDir, "tau-proxy call aws --"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Count(all, "tau-proxy call aws --") != 1 {
		t.Errorf("two proxies with one command wrote two shims:\n%s", all)
	}
	if strings.Contains(all, "aws-prod --") || strings.Contains(all, "call aws-dev") {
		t.Errorf("a shim is named after a proxy, not the command:\n%s", all)
	}
}

func TestCheckRunsBeforeAnythingHappens(t *testing.T) {
	f := &fakeDocker{}
	s := supervisor(t, f)
	delete(s.Host.Proxies.Packages, "aws-cli")
	if err := s.Start(); err == nil {
		t.Error("a missing package was accepted")
	}
	if len(f.calls) != 0 {
		t.Errorf("docker was called: %v", f.calls)
	}
}

func TestEachSocketDirectoryIsClosedToEveryoneButItsUserAndTheClients(t *testing.T) {
	f := &fakeDocker{}
	supervisor(t, f).Start()
	var script string
	for _, s := range f.stdin {
		if strings.Contains(s, "useradd") {
			script = s
		}
	}
	want := `install -d -m 0750 -o "$2" -g ` + ClientsGroup + ` ` + SocketDir + `/"$1"`
	if !strings.Contains(script, want) {
		t.Errorf("the socket directory is not mode 0750 for the user and the clients group:\n%s", script)
	}
}
