// Package dockercli runs the docker CLI. Errors keep docker's own message,
// because callers tell "no such container" from "daemon not running" by it.
package dockercli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// CLI runs docker. The zero value uses the process's own terminal streams
// for Run.
type CLI struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// Output runs `docker <args>` and returns its stdout. On failure the error
// includes docker's stderr.
func (CLI) Output(args ...string) (string, error) {
	cmd := exec.Command("docker", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return out.String(), fmt.Errorf("docker %s: %s", args[0], msg)
		}
		return out.String(), fmt.Errorf("docker %s: %w", args[0], err)
	}
	return out.String(), nil
}

// Run runs `docker <args>` attached to the terminal, as `docker exec -it` needs.
func (c CLI) Run(args ...string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	if c.Stdin == nil {
		cmd.Stdin = os.Stdin
	}
	if c.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	if c.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

// WithStdin runs `docker <args>` with text on stdin and both output streams
// sent to out. BuildKit is required for the heredocs in generated Dockerfiles.
func (CLI) WithStdin(args []string, text string, out io.Writer) error {
	cmd := exec.Command("docker", args...)
	cmd.Env = append(os.Environ(), "DOCKER_BUILDKIT=1")
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w", args[0], err)
	}
	return nil
}

// Exec runs `docker <args>` with stdin and returns its stdout. Secrets travel
// on stdin so they never appear in a process list.
func (CLI) Exec(stdin io.Reader, args ...string) (string, error) {
	cmd := exec.Command("docker", args...)
	var out, errb bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &out, &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return out.String(), fmt.Errorf("docker %s: %s", args[0], msg)
		}
		return out.String(), fmt.Errorf("docker %s: %w", args[0], err)
	}
	return out.String(), nil
}
