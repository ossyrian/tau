package proxy

import "testing"

func TestDeny(t *testing.T) {
	cases := []struct {
		rule string
		args []string
		deny bool
	}{
		{"configure export-credentials", []string{"configure", "export-credentials"}, true},
		{"configure export-credentials", []string{"--profile", "p", "configure", "export-credentials"}, true}, // a global flag in front does not hide it
		{"configure export-credentials", []string{"configure", "list"}, false},                                // words must be consecutive
		{"configure export-credentials", []string{"export-credentials", "configure"}, false},
		{"--debug", []string{"s3", "ls", "--debug=1"}, true}, // a flag rule matches --flag=value
		{"s3 ls", []string{"s3", "ls=x"}, false},             // a word rule does not
		{"s3 ls", []string{"S3", "LS"}, false},               // case matters
		{"s3 ls", []string{"s3", "cp", "a b s3 ls"}, false},  // a value is not searched
		{"", []string{"anything"}, false},
	}
	for _, c := range cases {
		err := Check([]string{c.rule}, c.args)
		if (err != nil) != c.deny {
			t.Errorf("rule %q on %v: denied=%v, want %v", c.rule, c.args, err != nil, c.deny)
		}
	}
	err := Check([]string{"configure export-credentials"}, []string{"configure", "export-credentials"})
	if err == nil || err.Error() != `refused: arguments match the deny rule "configure export-credentials"` {
		t.Errorf("got %v", err)
	}
}
