package dockercli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDocker puts a `docker` script first on PATH. It prints its arguments to
// stdout, and fails with a message on stderr when the first argument is "fail".
func fakeDocker(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = fail ]; then echo \"Error: No such object: tau\" >&2; echo partial; exit 1; fi\nprintf '%s|' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestOutputReturnsStdout(t *testing.T) {
	fakeDocker(t)
	out, err := CLI{}.Output("inspect", "tau")
	if err != nil || out != "inspect|tau|" {
		t.Errorf("out %q err %v", out, err)
	}
}

func TestOutputErrorCarriesStderr(t *testing.T) {
	fakeDocker(t)
	out, err := CLI{}.Output("fail")
	if err == nil || !strings.Contains(err.Error(), "No such object: tau") {
		t.Fatalf("error lacks docker's message: %v", err)
	}
	if out != "partial\n" {
		t.Errorf("stdout lost: %q", out)
	}
}

func TestOutputWhenDockerIsMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := (CLI{}).Output("x"); err == nil {
		t.Error("want error")
	}
}

func TestRunAttachesTheGivenStreams(t *testing.T) {
	fakeDocker(t)
	var out, errb strings.Builder
	c := CLI{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb}
	if err := c.Run("a", "b"); err != nil || out.String() != "a|b|" {
		t.Errorf("out %q err %v", out.String(), err)
	}
	if err := c.Run("fail"); err == nil || !strings.Contains(errb.String(), "No such object") {
		t.Errorf("err %v stderr %q", err, errb.String())
	}
}

func TestWithStdinSendsTheTextAndStreamsOutput(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\ncat\n"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out strings.Builder
	if err := (CLI{}).WithStdin([]string{"build"}, "FROM x\n", &out); err != nil || out.String() != "FROM x\n" {
		t.Errorf("out %q err %v", out.String(), err)
	}
}

func TestWithStdinSetsBuildKit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\necho $DOCKER_BUILDKIT\n"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out strings.Builder
	(CLI{}).WithStdin([]string{"build"}, "", &out)
	if out.String() != "1\n" {
		t.Errorf("got %q", out.String())
	}
}

func TestWithStdinFailureNamesTheCommand(t *testing.T) {
	fakeDocker(t)
	var out strings.Builder
	err := (CLI{}).WithStdin([]string{"fail"}, "", &out)
	if err == nil || !strings.Contains(err.Error(), "docker fail") {
		t.Errorf("got %v", err)
	}
}

// echoDocker puts a `docker` on PATH that prints its arguments, then its stdin.
func echoDocker(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = fail ]; then echo \"Error: no such container\" >&2; exit 1; fi\nprintf 'args:%s|' \"$@\"\necho\ncat\n"
	os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestExecSendsStdinAndReturnsStdout(t *testing.T) {
	echoDocker(t)
	out, err := CLI{}.Exec(strings.NewReader("the-secret-document"), "exec", "-i", "tau")
	if err != nil || !strings.Contains(out, "the-secret-document") || !strings.Contains(out, "args:exec|args:-i|args:tau|") {
		t.Errorf("out %q err %v", out, err)
	}
}

func TestExecKeepsTheSecretOutOfTheArguments(t *testing.T) {
	echoDocker(t)
	out, _ := CLI{}.Exec(strings.NewReader("TOP-SECRET"), "exec", "-i", "tau")
	args := strings.SplitN(out, "\n", 2)[0]
	if strings.Contains(args, "TOP-SECRET") {
		t.Errorf("the secret is in the arguments: %q", args)
	}
}

func TestExecErrorCarriesStderr(t *testing.T) {
	echoDocker(t)
	_, err := CLI{}.Exec(nil, "fail")
	if err == nil || !strings.Contains(err.Error(), "no such container") {
		t.Errorf("got %v", err)
	}
}
