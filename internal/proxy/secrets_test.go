package proxy

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func store(t *testing.T) *Secrets { return &Secrets{Dir: filepath.Join(t.TempDir(), "secrets")} }

func TestBadNamesAreRefused(t *testing.T) {
	s := store(t)
	for _, p := range []string{"", "../x", "a/b", ".hidden", "A B"} {
		if err := s.Set(p, "K", "v"); err == nil {
			t.Errorf("proxy name %q accepted", p)
		}
	}
	for _, k := range []string{"", "1A", "A-B", "A B", "A=B"} {
		if err := s.Set("p", k, "v"); err == nil {
			t.Errorf("key %q accepted", k)
		}
	}
}

func TestMergeEnvPutsMintedValuesOverStoredOnes(t *testing.T) {
	got := MergeEnv([]Var{{Key: "A", Value: "stored"}, {Key: "B", Value: "only-stored"}}, []Var{{Key: "A", Value: "minted"}, {Key: "C", Value: "only-minted"}})
	want := map[string]string{"A": "minted", "B": "only-stored", "C": "only-minted"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestParseMintedReadsKeyValueLinesAndExportPrefix(t *testing.T) {
	out := "export AWS_ACCESS_KEY_ID=AKIA\nAWS_SECRET_ACCESS_KEY=abc=def\n\n# comment\nnot a pair\nexport  X = 1\n"
	got := ParseMinted(out)
	want := []Var{{Key: "AWS_ACCESS_KEY_ID", Value: "AKIA"}, {Key: "AWS_SECRET_ACCESS_KEY", Value: "abc=def"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestMintRunsTheCommandAndParsesItsOutput(t *testing.T) {
	s := store(t)
	s.Run = func(cmd string) (string, error) {
		if cmd != "mint it" {
			t.Errorf("ran %q", cmd)
		}
		return "export TOKEN=fresh\n", nil
	}
	got, err := s.Mint("mint it")
	if err != nil || !reflect.DeepEqual(got, []Var{{Key: "TOKEN", Value: "fresh"}}) {
		t.Errorf("got %v %v", got, err)
	}
	s.Run = func(string) (string, error) { return "", errors.New("sso expired") }
	if _, err := s.Mint("x"); err == nil || !strings.Contains(err.Error(), "sso expired") {
		t.Errorf("got %v", err)
	}
	if got, err := s.Mint(""); err != nil || got != nil {
		t.Errorf("an empty command must mint nothing: %v %v", got, err)
	}
}
