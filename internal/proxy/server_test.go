package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// start runs a Server on a temp socket. uid is what every connection claims to be.
// uidNow is the uid every test connection claims; tests change it while the server runs.
var uidNow atomic.Int64

func start(t *testing.T, cfg Config, uid int) (*Server, *http.Client) {
	t.Helper()
	// Unix socket paths are limited to about 100 bytes, so not t.TempDir().
	dir, err := os.MkdirTemp("", "tp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "p.sock")
	s := &Server{
		Config: cfg, Home: t.TempDir(), Path: "/usr/bin:/bin",
		Timeout: 5 * time.Second, MaxOutput: 1 << 16,
		Peer: func(net.Conn) (int, error) { return int(uidNow.Load()), nil },
	}
	uidNow.Store(int64(uid))
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: s.Handler(), ConnContext: s.ConnContext}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return s, &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
}

func run(t *testing.T, c *http.Client, name string, req RunRequest) (RunResponse, int) {
	t.Helper()
	body, _ := json.Marshal(req)
	resp, err := c.Post("http://tau/v1/run/"+name, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out RunResponse
	json.NewDecoder(resp.Body).Decode(&out)
	return out, resp.StatusCode
}

var cfg = Config{
	Proxies: map[string]Spec{
		"echo": {Exec: bin("echo"), AgentCommand: "echo", Description: "echo", Env: map[string]string{"SECRET": "s3cr3t-value"}},
		"env":  {Exec: bin("env"), Env: map[string]string{"SECRET": "s3cr3t-value", "OTHER": "x"}},
		"cat":  {Exec: bin("cat")},
		"sh":   {Exec: bin("sh"), Deny: []string{"-x"}},
		"pwd":  {Exec: bin("pwd")},
	},
	Users: map[int][]string{1000: {"echo", "env", "cat", "sh", "pwd"}, 2000: {"echo"}},
}

func TestARunReturnsOutputAndExitCode(t *testing.T) {
	_, c := start(t, cfg, 1000)
	r, code := run(t, c, "echo", RunRequest{Args: []string{"hello", "world"}})
	if code != 200 || string(r.Stdout) != "hello world\n" || r.Exit != 0 {
		t.Errorf("got %d %+v", code, r)
	}
	r, _ = run(t, c, "sh", RunRequest{Args: []string{"-c", "echo oops >&2; exit 3"}})
	if r.Exit != 3 || string(r.Stderr) != "oops\n" {
		t.Errorf("exit/stderr: %+v", r)
	}
}

func TestStdinReachesTheCommand(t *testing.T) {
	_, c := start(t, cfg, 1000)
	r, _ := run(t, c, "cat", RunRequest{Stdin: []byte("piped in")})
	if string(r.Stdout) != "piped in" {
		t.Errorf("got %q", r.Stdout)
	}
}

func TestTheCommandGetsTheSecretsAndNothingElse(t *testing.T) {
	_, c := start(t, cfg, 1000)
	r, _ := run(t, c, "env", RunRequest{})
	lines := strings.Split(strings.TrimSpace(string(r.Stdout)), "\n")
	got := map[string]string{}
	for _, l := range lines {
		k, v, _ := strings.Cut(l, "=")
		got[k] = v
	}
	if got["SECRET"] != "s3cr3t-value" || got["OTHER"] != "x" {
		t.Errorf("secrets missing: %v", got)
	}
	for k := range got {
		switch k {
		case "SECRET", "OTHER", "PATH", "HOME", "TMPDIR", "LANG", "PWD", "SHLVL", "_", "OLDPWD":
		default:
			t.Errorf("unexpected variable %s", k)
		}
	}
	if got["PATH"] != "/usr/bin:/bin" {
		t.Errorf("PATH %q", got["PATH"])
	}
}

func TestEachCallRunsInItsOwnPrivateDirectoryThatIsRemoved(t *testing.T) {
	s, c := start(t, cfg, 1000)
	a, _ := run(t, c, "pwd", RunRequest{})
	b, _ := run(t, c, "pwd", RunRequest{})
	da, db := strings.TrimSpace(string(a.Stdout)), strings.TrimSpace(string(b.Stdout))
	home, _ := filepath.EvalSymlinks(s.Home) // /var is a link on macOS
	if da == db || !strings.HasPrefix(da, home) {
		t.Errorf("dirs %q %q (home %q)", da, db, s.Home)
	}
	if exists(da) || exists(db) {
		t.Error("a call directory was left behind")
	}
}

func TestAProfileCanOnlyCallItsActiveProxies(t *testing.T) {
	_, c := start(t, cfg, 2000)
	if _, code := run(t, c, "echo", RunRequest{Args: []string{"x"}}); code != 200 {
		t.Errorf("active proxy refused: %d", code)
	}
	for _, name := range []string{"env", "cat", "ghost"} {
		r, code := run(t, c, name, RunRequest{})
		if code != 403 || !strings.Contains(r.Error, "not available") {
			t.Errorf("%s: %d %+v", name, code, r)
		}
	}
}

func TestAFailedPeerLookupIsRefused(t *testing.T) {
	s := &Server{Config: cfg, Home: t.TempDir(), Path: "/usr/bin:/bin", Timeout: time.Second, MaxOutput: 100,
		Peer: func(net.Conn) (int, error) { return 0, net.ErrClosed }}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/run/echo", strings.NewReader("{}"))
	req = req.WithContext(s.ConnContext(req.Context(), nil))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "cannot tell who is calling") {
		t.Errorf("got %d %s", rec.Code, rec.Body)
	}
}

func TestDenyRulesRefuseBeforeAnythingRuns(t *testing.T) {
	_, c := start(t, cfg, 1000)
	r, code := run(t, c, "sh", RunRequest{Args: []string{"-x", "-c", "echo should-not-run"}})
	if code != 403 || !strings.Contains(r.Error, "deny rule") || len(r.Stdout) != 0 {
		t.Errorf("got %d %+v", code, r)
	}
}

func TestListShowsOnlyTheCallersProxiesWithoutSecrets(t *testing.T) {
	_, c := start(t, cfg, 2000)
	resp, _ := c.Get("http://tau/v1/proxies")
	raw := new(bytes.Buffer)
	raw.ReadFrom(resp.Body)
	var list []Listing
	json.Unmarshal(raw.Bytes(), &list)
	if len(list) != 1 || list[0].Name != "echo" || list[0].AgentCommand != "echo" {
		t.Errorf("got %v", list)
	}
	if strings.Contains(raw.String(), "s3cr3t") {
		t.Errorf("a secret leaked into the listing: %s", raw)
	}
}

func TestTimeoutKillsTheWholeProcessGroup(t *testing.T) {
	s, c := start(t, cfg, 1000)
	s.Timeout = 300 * time.Millisecond
	begin := time.Now()
	r, code := run(t, c, "sh", RunRequest{Args: []string{"-c", "sleep 30 & sleep 30"}})
	if time.Since(begin) > 5*time.Second {
		t.Fatalf("took %v", time.Since(begin))
	}
	if code != 200 || r.Exit == 0 || !r.TimedOut {
		t.Errorf("got %d %+v", code, r)
	}
}

func TestOutputIsCappedAndSaysSo(t *testing.T) {
	s, c := start(t, cfg, 1000)
	s.MaxOutput = 100
	r, _ := run(t, c, "sh", RunRequest{Args: []string{"-c", "head -c 5000 /dev/zero | tr '\\0' a"}})
	if len(r.Stdout) != 100 || !r.Truncated {
		t.Errorf("len %d truncated %v", len(r.Stdout), r.Truncated)
	}
}

func TestOnlyPOSTRunsAndBodiesAreChecked(t *testing.T) {
	_, c := start(t, cfg, 1000)
	resp, _ := c.Get("http://tau/v1/run/echo")
	if resp.StatusCode != 405 {
		t.Errorf("GET run: %d", resp.StatusCode)
	}
	resp, _ = c.Post("http://tau/v1/run/echo", "application/json", strings.NewReader("{not json"))
	if resp.StatusCode != 400 {
		t.Errorf("bad body: %d", resp.StatusCode)
	}
	resp, _ = c.Post("http://tau/v1/run/echo", "application/json", strings.NewReader(strings.Repeat("a", 5<<20)))
	if resp.StatusCode != 413 && resp.StatusCode != 400 {
		t.Errorf("huge body: %d", resp.StatusCode)
	}
}

func TestOnlyTheAdminMayReload(t *testing.T) {
	s, c := start(t, cfg, 1000)
	s.Admin = 1001
	body, _ := json.Marshal(Bootstrap{Proxies: map[string]Spec{"evil": {Exec: bin("sh")}}, Users: map[string][]string{"1000": {"evil"}}})
	resp, _ := c.Post("http://tau/v1/reload", "application/json", bytes.NewReader(body))
	if resp.StatusCode != 403 {
		t.Errorf("reload by a non-admin: %d", resp.StatusCode)
	}
	if _, code := run(t, c, "evil", RunRequest{}); code != 403 {
		t.Errorf("the rejected config took effect: %d", code)
	}
	if _, code := run(t, c, "echo", RunRequest{}); code != 200 {
		t.Error("the old config was lost")
	}
}

func TestReloadIsOffWhenNoAdminIsSet(t *testing.T) {
	_, c := start(t, cfg, 0) // uid 0 and Admin 0 must not match by accident
	resp, _ := c.Post("http://tau/v1/reload", "application/json", strings.NewReader("{}"))
	if resp.StatusCode != 403 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestABadReloadKeepsTheOldConfig(t *testing.T) {
	s, c := start(t, cfg, 1000)
	s.Admin = 1000
	resp, _ := c.Post("http://tau/v1/reload", "application/json", strings.NewReader(`{"users":{"pi":["x"]}}`))
	if resp.StatusCode != 400 {
		t.Errorf("got %d", resp.StatusCode)
	}
	if _, code := run(t, c, "echo", RunRequest{}); code != 200 {
		t.Error("a bad reload broke the running config")
	}
}

func TestAProxyWithCwdRunsInTheCallersDirectoryInsideItsWorkspace(t *testing.T) {
	ws := t.TempDir()
	ws, _ = filepath.EvalSymlinks(ws)
	sub := filepath.Join(ws, "proj")
	os.MkdirAll(sub, 0o755)
	c2 := Config{
		Proxies: map[string]Spec{"pwd": {Exec: bin("pwd"), InWorkspace: true}},
		Users:   map[int][]string{1000: {"pwd"}},
		Roots:   map[int][]string{1000: {ws}},
	}
	_, c := start(t, c2, 1000)
	for _, dir := range []string{sub, ws} {
		r, code := run(t, c, "pwd", RunRequest{Cwd: dir})
		if code != 200 || strings.TrimSpace(string(r.Stdout)) != dir {
			t.Errorf("cwd %s: %d %+v", dir, code, r)
		}
	}
}

func TestACwdOutsideTheWorkspaceIsRefused(t *testing.T) {
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	other, _ := filepath.EvalSymlinks(t.TempDir())
	link := filepath.Join(ws, "escape")
	os.Symlink(other, link)
	c2 := Config{
		Proxies: map[string]Spec{"pwd": {Exec: bin("pwd"), InWorkspace: true}},
		Users:   map[int][]string{1000: {"pwd"}},
		Roots:   map[int][]string{1000: {ws}},
	}
	_, c := start(t, c2, 1000)
	for _, dir := range []string{other, "/", "/etc", ws + "/../x", link, ws + "x", "relative/path", ""} {
		r, code := run(t, c, "pwd", RunRequest{Cwd: dir})
		if code != 403 {
			t.Errorf("cwd %q accepted: %d %+v", dir, code, r)
		}
	}
}

func TestCommandsRunWithAGroupWritableUmask(t *testing.T) {
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	c2 := Config{
		Proxies: map[string]Spec{"sh": {Exec: bin("sh"), InWorkspace: true}},
		Users:   map[int][]string{1000: {"sh"}},
		Roots:   map[int][]string{1000: {ws}},
	}
	_, c := start(t, c2, 1000)
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	GroupWritable()
	r, _ := run(t, c, "sh", RunRequest{Cwd: ws, Args: []string{"-c", "umask"}})
	if strings.TrimSpace(string(r.Stdout)) != "0002" {
		t.Errorf("umask %q", r.Stdout)
	}
}

func TestHomeAndTmpAreNotTheWorkspaceAndAreRemovedAfterTheCall(t *testing.T) {
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	c2 := Config{
		Proxies: map[string]Spec{"sh": {Exec: bin("sh"), InWorkspace: true}},
		Users:   map[int][]string{1000: {"sh"}},
		Roots:   map[int][]string{1000: {ws}},
	}
	_, c := start(t, c2, 1000)
	r, _ := run(t, c, "sh", RunRequest{Cwd: ws, Args: []string{"-c", "echo $HOME; echo x > $HOME/cache"}})
	home := strings.TrimSpace(string(r.Stdout))
	if home == ws || home == "" || exists(home) {
		t.Errorf("HOME %q (workspace %q): must be private and gone after the call", home, ws)
	}
	if entries, _ := os.ReadDir(ws); len(entries) != 0 {
		t.Errorf("the call wrote into the workspace: %v", entries)
	}
}

// bin finds a program the test can run, as an absolute path.
func bin(name string) string {
	for _, d := range []string{"/bin", "/usr/bin"} {
		if st, err := os.Stat(filepath.Join(d, name)); err == nil && !st.IsDir() {
			return filepath.Join(d, name)
		}
	}
	panic("no " + name)
}

func TestTheCommandRunsFromItsAbsolutePathNotASearch(t *testing.T) {
	shims, real := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(shims, "tool"), []byte("#!/bin/sh\necho SHIM\n"), 0o755)
	os.WriteFile(filepath.Join(real, "tool"), []byte("#!/bin/sh\necho REAL\n"), 0o755)
	t.Setenv("PATH", shims+":/usr/bin:/bin") // the process PATH has a shim of the same name first

	c2 := Config{Proxies: map[string]Spec{"tool": {Exec: filepath.Join(real, "tool")}}, Users: map[int][]string{1000: {"tool"}}}
	_, c := start(t, c2, 1000)
	r, code := run(t, c, "tool", RunRequest{})
	if code != 200 || strings.TrimSpace(string(r.Stdout)) != "REAL" {
		t.Errorf("ran %q (code %d)", r.Stdout, code)
	}
}

func TestARelativeExecIsRefusedSoNothingIsLookedUp(t *testing.T) {
	only := t.TempDir()
	os.WriteFile(filepath.Join(only, "hidden-tool"), []byte("#!/bin/sh\necho LEAK\n"), 0o755)
	t.Setenv("PATH", only+":/usr/bin:/bin")
	c2 := Config{Proxies: map[string]Spec{"h": {Exec: "hidden-tool"}}, Users: map[int][]string{1000: {"h"}}}
	_, c := start(t, c2, 1000)
	r, code := run(t, c, "h", RunRequest{})
	if code != 502 || strings.Contains(string(r.Stdout), "LEAK") || !strings.Contains(r.Error, "absolute") {
		t.Errorf("got %d %+v", code, r)
	}
}

func TestAMissingProgramNamesItsPath(t *testing.T) {
	_, c := start(t, Config{Proxies: map[string]Spec{"n": {Exec: "/nonexistent/prog"}}, Users: map[int][]string{1000: {"n"}}}, 1000)
	r, code := run(t, c, "n", RunRequest{})
	if code != 502 || !strings.Contains(r.Error, "/nonexistent/prog") {
		t.Errorf("got %d %+v", code, r)
	}
}

func TestMintedValuesBeatStoredOnesInTheEnvironment(t *testing.T) {
	c2 := Config{Proxies: map[string]Spec{"env": {Exec: bin("env"), Env: map[string]string{"K": "stored", "ONLY": "s"}, Minted: map[string]string{"K": "minted"}}}, Users: map[int][]string{1000: {"env"}}}
	_, c := start(t, c2, 1000)
	r, _ := run(t, c, "env", RunRequest{})
	out := string(r.Stdout)
	if !strings.Contains(out, "K=minted\n") || strings.Contains(out, "K=stored") || !strings.Contains(out, "ONLY=s\n") {
		t.Errorf("env: %s", out)
	}
}

func reloadWith(t *testing.T, c *http.Client, b Bootstrap) {
	t.Helper()
	body, _ := json.Marshal(b)
	resp, err := c.Post("http://tau/v1/reload", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("reload: %v %v", resp, err)
	}
}

func envOf(t *testing.T, c *http.Client) string {
	t.Helper()
	r, code := run(t, c, "env", RunRequest{})
	if code != 200 {
		t.Fatalf("run: %d %+v", code, r)
	}
	return string(r.Stdout)
}

func TestAReloadWithoutMintedValuesKeepsTheOnesAlreadyHeld(t *testing.T) {
	c2 := Config{Proxies: map[string]Spec{"env": {Exec: bin("env"), Env: map[string]string{"R": "us"}, Minted: map[string]string{"TOKEN": "fresh"}}}, Users: map[int][]string{1000: {"env"}}}
	s, c := start(t, c2, 1000)
	s.Admin = 1000
	// A stored variable changed; the document carries no minted values (nil).
	reloadWith(t, c, Bootstrap{Proxies: map[string]Spec{"env": {Exec: bin("env"), Env: map[string]string{"R": "eu"}}}, Users: map[string][]string{"1000": {"env"}}})
	out := envOf(t, c)
	if !strings.Contains(out, "R=eu\n") || !strings.Contains(out, "TOKEN=fresh\n") {
		t.Errorf("env: %s", out)
	}
}

func TestHeldMintedValuesDoNotCarryToADifferentProgram(t *testing.T) {
	c2 := Config{Proxies: map[string]Spec{"env": {Exec: bin("env"), Minted: map[string]string{"TOKEN": "old"}}}, Users: map[int][]string{1000: {"env"}}}
	s, c := start(t, c2, 1000)
	s.Admin = 1000
	// The proxy now runs a different program: credentials minted for the old one must not follow.
	reloadWith(t, c, Bootstrap{Proxies: map[string]Spec{"env": {Exec: bin("sh")}}, Users: map[string][]string{"1000": {"env"}}})
	r, _ := run(t, c, "env", RunRequest{Args: []string{"-c", "echo T=$TOKEN"}})
	if strings.Contains(string(r.Stdout), "old") {
		t.Errorf("minted values followed a changed program: %s", r.Stdout)
	}
}

func TestASiblingDirectoryWithTheSamePrefixIsOutsideTheWorkspace(t *testing.T) {
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	ws := filepath.Join(parent, "ws")
	sibling := filepath.Join(parent, "ws2") // exists, and starts with the root's name
	os.MkdirAll(ws, 0o755)
	os.MkdirAll(sibling, 0o755)
	if _, err := Within([]string{ws}, sibling); err == nil {
		t.Errorf("%s was accepted as inside %s", sibling, ws)
	}
	if _, err := Within([]string{ws}, ws); err != nil {
		t.Errorf("the root itself was refused: %v", err)
	}
}

func TestATimeoutLeavesNoChildBehind(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child-survived")
	s, c := start(t, cfg, 1000)
	s.Timeout = 300 * time.Millisecond
	// The child sleeps past the timeout, then makes the marker. If only the shell
	// were killed, the child would live on and make it.
	script := "(sleep 1.5; touch " + marker + ") & sleep 30"
	r, _ := run(t, c, "sh", RunRequest{Args: []string{"-c", script}})
	if !r.TimedOut {
		t.Fatalf("did not time out: %+v", r)
	}
	time.Sleep(2500 * time.Millisecond)
	if exists(marker) {
		t.Error("a child process outlived the timeout")
	}
}

func exists(path string) bool {
	_, err := os.Stat(filepath.Clean(path))
	return err == nil
}
