package proxy

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serve starts a real Server on a socket and returns a Client for it.
func serve(t *testing.T, c Config, uid int) *Client {
	t.Helper()
	dir, _ := os.MkdirTemp("", "cl")
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	uidNow.Store(int64(uid))
	s := &Server{Config: c, Home: t.TempDir(), Path: "/usr/bin:/bin", Timeout: 5 * time.Second, MaxOutput: 1 << 16,
		Peer: func(net.Conn) (int, error) { return int(uidNow.Load()), nil }}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: s.Handler(), ConnContext: s.ConnContext}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return &Client{Socket: sock}
}

func TestClientPassesOutputThroughAndReturnsTheExitCode(t *testing.T) {
	cl := serve(t, cfg, 1000)
	var out, errb bytes.Buffer
	code, err := cl.Call(context.Background(), "sh", []string{"-c", "echo out; echo err >&2; exit 7"}, nil, &out, &errb)
	if err != nil || code != 7 || out.String() != "out\n" || errb.String() != "err\n" {
		t.Errorf("code=%d err=%v out=%q err=%q", code, err, out.String(), errb.String())
	}
}

func TestClientSendsStdin(t *testing.T) {
	cl := serve(t, cfg, 1000)
	var out bytes.Buffer
	code, err := cl.Call(context.Background(), "cat", nil, strings.NewReader("hello"), &out, &bytes.Buffer{})
	if err != nil || code != 0 || out.String() != "hello" {
		t.Errorf("code=%d err=%v out=%q", code, err, out.String())
	}
}

func TestClientReportsARefusalAsAnError(t *testing.T) {
	cl := serve(t, cfg, 2000)
	_, err := cl.Call(context.Background(), "cat", nil, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("got %v", err)
	}
	cl = serve(t, cfg, 1000)
	_, err = cl.Call(context.Background(), "sh", []string{"-x"}, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "deny rule") {
		t.Errorf("got %v", err)
	}
}

func TestClientSaysSoWhenOutputWasCutOrTimedOut(t *testing.T) {
	cl := serve(t, cfg, 1000)
	var errb bytes.Buffer
	cl.Call(context.Background(), "sh", []string{"-c", "head -c 100000 /dev/zero | tr '\\0' a"}, nil, &bytes.Buffer{}, &errb)
	if !strings.Contains(errb.String(), "truncated") {
		t.Errorf("stderr %q", errb.String())
	}
}

func TestClientWithNoServerGivesAHelpfulError(t *testing.T) {
	cl := &Client{Socket: filepath.Join(t.TempDir(), "none")}
	_, err := cl.Call(context.Background(), "aws", nil, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "proxy") {
		t.Errorf("got %v", err)
	}
}

func TestClientReloadSendsADocumentOnlyTheAdminMayChange(t *testing.T) {
	dir, _ := os.MkdirTemp("", "cl")
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s")
	uidNow.Store(1000)
	s := &Server{Config: cfg, Home: t.TempDir(), Path: "/usr/bin:/bin", Timeout: 5 * time.Second, MaxOutput: 1 << 16,
		Admin: 1000, Peer: func(net.Conn) (int, error) { return int(uidNow.Load()), nil }}
	ln, _ := net.Listen("unix", sock)
	srv := &http.Server{Handler: s.Handler(), ConnContext: s.ConnContext}
	go srv.Serve(ln)
	defer srv.Close()
	cl := &Client{Socket: sock}

	doc := `{"proxies":{"echo":{"command":"echo","env":{"N":"2"}}},"users":{"1000":["echo"]}}`
	if err := cl.Reload(context.Background(), strings.NewReader(doc)); err != nil {
		t.Fatal(err)
	}
	l, _ := cl.List(context.Background())
	if len(l) != 1 || l[0].Name != "echo" {
		t.Errorf("reload not applied: %v", l)
	}
	uidNow.Store(2000)
	if err := cl.Reload(context.Background(), strings.NewReader(doc)); err == nil {
		t.Error("a non-admin reloaded")
	}
}

func TestStdinForPassesAPipeAndAFileButNotATerminal(t *testing.T) {
	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()
	if StdinFor(r) == nil {
		t.Error("a pipe must be forwarded")
	}
	f, _ := os.CreateTemp(t.TempDir(), "in")
	defer f.Close()
	if StdinFor(f) == nil {
		t.Error("a redirected file must be forwarded")
	}
	if StdinFor(nil) != nil {
		t.Error("no stdin must give none")
	}
}
