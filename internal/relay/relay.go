// Package relay runs a command, such as `docker exec -it`, on the user's
// terminal and watches the keys for Ctrl+V. Pi pastes an image on Ctrl+V by
// reading the clipboard of the machine it runs on, which here is a container
// with no clipboard. So before the key goes on, the relay copies the host
// clipboard to where the container can read it.
package relay

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/muesli/cancelreader"
	"golang.org/x/term"
)

// Clipboard is what the relay calls around a paste.
type Clipboard interface {
	// Paste makes the host clipboard available to the command. It returns
	// when that is done.
	Paste()
	// Clear removes what Paste made available.
	Clear()
}

// Relay runs cmd. It has the methods Bubble Tea's tea.Exec wants.
type Relay struct {
	cmd    *exec.Cmd
	clip   Clipboard
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// New returns a Relay for cmd on this process's own terminal streams. With a
// nil clip, or when stdin is not a terminal, it just runs cmd.
func New(cmd *exec.Cmd, clip Clipboard) *Relay {
	return &Relay{cmd: cmd, clip: clip, stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}
}

func (r *Relay) SetStdin(in io.Reader) { r.stdin = in }
func (r *Relay) SetStdout(w io.Writer) { r.stdout = w }
func (r *Relay) SetStderr(w io.Writer) { r.stderr = w }
func (r *Relay) terminal() (*os.File, bool) {
	f, ok := r.stdin.(*os.File)
	return f, ok && r.clip != nil && term.IsTerminal(int(f.Fd()))
}

// drainWait is how long to wait for the last output after the command exits.
const drainWait = time.Second

// Run runs the command and returns its exit error.
func (r *Relay) Run() error {
	in, ok := r.terminal()
	if !ok {
		r.cmd.Stdin, r.cmd.Stdout, r.cmd.Stderr = r.stdin, r.stdout, r.stderr
		return r.cmd.Run()
	}
	defer r.clip.Clear()

	// The command gets a terminal of its own, which is what `docker -t` needs.
	ptmx, err := pty.Start(r.cmd)
	if err != nil {
		return err
	}
	defer ptmx.Close()

	resize := make(chan os.Signal, 1)
	signal.Notify(resize, syscall.SIGWINCH)
	defer signal.Stop(resize)
	go func() {
		for range resize {
			_ = pty.InheritSize(in, ptmx)
		}
	}()
	_ = pty.InheritSize(in, ptmx)

	old, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(in.Fd()), old)

	keys, err := cancelreader.NewReader(in)
	if err != nil {
		return err
	}
	defer keys.Close()
	keysDone := make(chan struct{})
	go func() {
		defer close(keysDone)
		r.forward(keys, ptmx)
	}()

	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		_, _ = io.Copy(r.stdout, ptmx)
	}()

	err = r.cmd.Wait()
	// Hand the keyboard back before anything else reads it.
	keys.Cancel()
	<-keysDone
	select {
	case <-outDone:
	case <-time.After(drainWait):
	}
	return err
}

// forward copies keys to the command. A paste key first syncs the clipboard.
func (r *Relay) forward(keys io.Reader, to io.Writer) {
	buf := make([]byte, 4096)
	for {
		n, err := keys.Read(buf)
		if n > 0 {
			if isPaste(buf[:n]) {
				r.clip.Paste()
			}
			if _, werr := to.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// pasteKey matches Ctrl+V as a plain byte, or as the terminal sends it once
// tmux has asked for extended keys: kitty `CSI 118;5u` (a key release has
// event type 3 and does not match) and xterm `CSI 27;5;118~`.
var pasteKey = regexp.MustCompile(`\x16|\x1b\[118(?::\d*){0,2};5(?::[12])?(?:;[\d:]*)?u|\x1b\[27;5;118~`)

func isPaste(b []byte) bool { return pasteKey.Match(b) }
