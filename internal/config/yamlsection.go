package config

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ensureMapping returns the mapping under key in root, creating it when the
// key is missing or empty. It returns nil when the key holds something else.
func ensureMapping(root *yaml.Node, key string) *yaml.Node {
	sec := findKey(root, key)
	switch {
	case sec == nil:
		sec = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, scalar(key), sec)
	case isNull(sec) || (sec.Kind == yaml.MappingNode && len(sec.Content) == 0):
		*sec = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	case sec.Kind != yaml.MappingNode:
		return nil
	}
	return sec
}

// setScalar sets key in the mapping m to a string, keeping the key's position
// and comments when it already exists.
func setScalar(m *yaml.Node, key, val string) {
	if i := keyIndex(m, key); i >= 0 {
		old := m.Content[i+1]
		n := str(val)
		n.HeadComment, n.LineComment, n.FootComment = old.HeadComment, old.LineComment, old.FootComment
		m.Content[i+1] = n
		return
	}
	m.Content = append(m.Content, scalar(key), str(val))
}

// rejectUnknownKeys fails when the mapping under section holds a key not in
// allowed. A typo in a security setting must not be ignored.
func rejectUnknownKeys(data []byte, section string, allowed ...string) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return err
	}
	sec := findKey(doc.Content[0], section)
	if sec == nil || sec.Kind != yaml.MappingNode {
		return nil
	}
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	var bad []string
	for i := 0; i < len(sec.Content); i += 2 {
		if k := sec.Content[i].Value; !ok[k] {
			bad = append(bad, k)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("%s has unknown key %s (allowed: %s)", section, strings.Join(bad, ", "), strings.Join(allowed, ", "))
}
