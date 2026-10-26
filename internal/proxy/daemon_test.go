package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapRejectsAUserThatIsNotANumber(t *testing.T) {
	b := Bootstrap{Users: map[string][]string{"pi": {"aws"}}}
	if _, err := b.Config(); err == nil {
		t.Error("accepted a user name where a uid is needed")
	}
}

func TestBootstrapRejectsGarbage(t *testing.T) {
	if _, err := ReadBootstrap(strings.NewReader("{nope")); err == nil {
		t.Error("accepted garbage")
	}
	if _, err := ReadBootstrap(strings.NewReader(strings.Repeat("a", 5<<20))); err == nil {
		t.Error("accepted an oversized document")
	}
}

func TestListenMakesASocketOnlyTheGivenGroupCanUse(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ls")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "p.sock")
	ln, err := Listen(path, os.Getgid())
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o660 {
		t.Errorf("mode %v, want 0660", st.Mode().Perm())
	}
}

func TestListenReplacesAStaleSocket(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ls")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "p.sock")
	os.WriteFile(path, nil, 0o600)
	ln, err := Listen(path, os.Getgid())
	if err != nil {
		t.Fatalf("a leftover file blocked the socket: %v", err)
	}
	ln.Close()
}
