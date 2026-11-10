package relay

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

type fakeClip struct {
	mu      sync.Mutex
	pastes  int
	cleared int
	onPaste func()
}

func (f *fakeClip) Paste() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pastes++
	if f.onPaste != nil {
		f.onPaste()
	}
}

func (f *fakeClip) Clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared++
}

func (f *fakeClip) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pastes, f.cleared
}

// terminal returns a fake user terminal: write keys to user, and the relay
// reads them from tty as its stdin.
func terminal(t *testing.T) (user, tty *os.File) {
	t.Helper()
	user, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty here: %v", err)
	}
	t.Cleanup(func() { user.Close(); tty.Close() })
	return user, tty
}

func TestIsPaste(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"\x16", true},
		{"hello\x16", true},
		{"\x1b[118;5u", true},
		{"\x1b[118;5:1u", true},
		{"\x1b[27;5;118~", true},
		{"\x1b[118;5:3u", false}, // key release
		{"\x1b[118;6u", false},   // ctrl+shift+v
		{"v", false},
		{"", false},
	} {
		if got := isPaste([]byte(c.in)); got != c.want {
			t.Errorf("isPaste(%q) = %v", c.in, got)
		}
	}
}

func TestCtrlVSyncsTheClipboardBeforeTheProgramSeesTheKey(t *testing.T) {
	user, tty := terminal(t)
	flag := filepath.Join(t.TempDir(), "image")
	clip := &fakeClip{onPaste: func() { os.WriteFile(flag, []byte("png"), 0o644) }}
	cmd := exec.Command("sh", "-c", `stty raw -echo; echo ready; head -c1 >/dev/null; if [ -f "$F" ]; then echo FOUND; else echo MISSING; fi`)
	cmd.Env = append(os.Environ(), "F="+flag)
	var out safeBuffer
	r := New(cmd, clip)
	r.SetStdin(tty)
	r.SetStdout(&out)
	done := make(chan error, 1)
	go func() { done <- r.Run() }()

	waitFor(t, &out, "ready")
	user.Write([]byte{0x16})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "FOUND") {
		t.Errorf("the program ran before the clipboard was synced: %q", out.String())
	}
	if p, c := clip.counts(); p != 1 || c != 1 {
		t.Errorf("pastes %d cleared %d, want 1 and 1", p, c)
	}
}

func TestOtherKeysDoNotTouchTheClipboard(t *testing.T) {
	user, tty := terminal(t)
	clip := &fakeClip{}
	cmd := exec.Command("sh", "-c", `stty raw -echo; echo ready; head -c1`)
	var out safeBuffer
	r := New(cmd, clip)
	r.SetStdin(tty)
	r.SetStdout(&out)
	done := make(chan error, 1)
	go func() { done <- r.Run() }()

	waitFor(t, &out, "ready")
	user.Write([]byte("x"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "x") {
		t.Errorf("the key did not reach the program: %q", out.String())
	}
	if p, _ := clip.counts(); p != 0 {
		t.Errorf("pastes %d, want 0", p)
	}
}

func TestWithoutATerminalTheCommandRunsDirectly(t *testing.T) {
	clip := &fakeClip{}
	r := New(exec.Command("cat"), clip)
	r.SetStdin(strings.NewReader("piped\x16"))
	var out bytes.Buffer
	r.SetStdout(&out)
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "piped\x16" {
		t.Errorf("out %q", out.String())
	}
	if p, _ := clip.counts(); p != 0 {
		t.Errorf("pastes %d, want 0", p)
	}
}

func TestNewUsesTheProcessTerminal(t *testing.T) {
	r := New(exec.Command("true"), &fakeClip{})
	if r.stdin != os.Stdin || r.stdout != os.Stdout || r.stderr != os.Stderr {
		t.Error("a relay nobody configured does not use the process's streams")
	}
}

func TestWithoutAClipboardTheCommandRunsDirectly(t *testing.T) {
	r := New(exec.Command("echo", "hi"), nil)
	var out bytes.Buffer
	r.SetStdout(&out)
	if err := r.Run(); err != nil || out.String() != "hi\n" {
		t.Errorf("out %q err %v", out.String(), err)
	}
}

func TestTheExitStatusComesBack(t *testing.T) {
	_, tty := terminal(t)
	r := New(exec.Command("sh", "-c", "exit 3"), &fakeClip{})
	r.SetStdin(tty)
	r.SetStdout(&safeBuffer{})
	err := r.Run()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 3 {
		t.Errorf("err %v", err)
	}
}

// safeBuffer is a bytes.Buffer that the test and the relay can use together.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitFor(t *testing.T, out *safeBuffer, want string) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if strings.Contains(out.String(), want) {
			return
		}
	}
	t.Fatalf("never saw %q in %q", want, out.String())
}
