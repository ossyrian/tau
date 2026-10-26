// Package proxy runs credentialed commands for an agent that must not hold the
// credentials. A server runs as its own OS user, keeps the secrets in memory,
// and runs each requested command with them; the agent only gets the output.
package proxy

import (
	"fmt"
	"strings"
)

// Check refuses args when they match any rule. A rule is one or more words; it
// matches when those words appear as consecutive arguments, anywhere in the
// list, so a global flag in front does not hide the subcommand. A flag rule
// such as "--debug" also matches "--debug=value".
func Check(rules, args []string) error {
	for _, rule := range rules {
		words := strings.Fields(rule)
		if len(words) > 0 && contains(args, words) {
			return fmt.Errorf("refused: arguments match the deny rule %q", strings.Join(words, " "))
		}
	}
	return nil
}

func contains(args, words []string) bool {
	for i := 0; i+len(words) <= len(args); i++ {
		ok := true
		for j, w := range words {
			if !same(args[i+j], w) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// same reports whether arg is word, or word=value for a flag word.
func same(arg, word string) bool {
	if arg == word {
		return true
	}
	return strings.HasPrefix(word, "-") && strings.HasPrefix(arg, word+"=")
}
