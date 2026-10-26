package cmd

import (
	"io"
	"os"
	"os/exec"
)

// Run executes args the way main does and returns the exit status: 0, or 1 when
// cobra reports an error.
//
// Cobra's own output (help, usage, "Error: ...") goes to the process's stdout
// and stderr, so those are swapped for pipes while the command runs and their
// content is added to the harness buffers.
func (a *App) Run(args []string) int {
	realOut, realErr := os.Stdout, os.Stderr
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	os.Stdout, os.Stderr = outW, errW
	outDone, errDone := drain(outR), drain(errR)

	root := a.NewRootCmd()
	root.SetArgs(append([]string{}, args...))
	err := root.Execute()

	os.Stdout, os.Stderr = realOut, realErr
	outW.Close()
	errW.Close()
	io.WriteString(a.Stdout, <-outDone)
	io.WriteString(a.Stderr, <-errDone)
	if err != nil {
		return 1
	}
	return 0
}

func drain(r *os.File) <-chan string {
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	return done
}

func commandIn(dir, name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return string(out), err
}
