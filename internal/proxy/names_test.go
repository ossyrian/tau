package proxy

import (
	"strings"
	"testing"
)

func TestEachProxyHasItsOwnUserSocketAndDirectory(t *testing.T) {
	if got := UserFor("aws-prod"); got != "tx-aws-prod" {
		t.Errorf("user %q", got)
	}
	if got := SocketFor("aws-prod"); got != "/run/tau-proxy/aws-prod/sock" {
		t.Errorf("socket %q", got)
	}
	if got := DirFor("aws-prod"); got != "/run/tau-proxy/aws-prod" {
		t.Errorf("dir %q", got)
	}
	if UserFor("aws-prod") == UserFor("aws-dev") || SocketFor("a") == SocketFor("b") {
		t.Error("two proxies share an identity")
	}
}

func TestAProxyUIDIsStableAndOutsideTheProfileAndFixedRanges(t *testing.T) {
	if UIDFor("aws") != UIDFor("aws") {
		t.Error("not stable")
	}
	for _, n := range []string{"aws", "git", "gh", "aws-prod", "a", "zzzzzzzzzzzzzzzzzzzzzzzz"} {
		if u := UIDFor(n); u < 30000 || u >= 50000 {
			t.Errorf("%s: uid %d outside 30000-49999", n, u)
		}
	}
}

func TestProxyUIDCollisionsAreReported(t *testing.T) {
	if err := CheckUIDs([]string{"aws", "git"}); err != nil {
		t.Errorf("distinct proxies refused: %v", err)
	}
	// Find two names that collide, then check the error names both.
	seen := map[int]string{}
	var a, b string
	for i := 0; i < 100000 && b == ""; i++ {
		n := "p" + strings.Repeat("x", i%7) + itoa(i)
		if prev, ok := seen[UIDFor(n)]; ok {
			a, b = prev, n
		}
		seen[UIDFor(n)] = n
	}
	err := CheckUIDs([]string{a, b})
	if err == nil || !strings.Contains(err.Error(), a) || !strings.Contains(err.Error(), b) {
		t.Errorf("got %v for %q %q", err, a, b)
	}
}
