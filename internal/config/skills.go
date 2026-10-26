package config

import (
	"errors"
	"fmt"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultSkillPath is searched for skills when config.yaml sets no
// skills.paths.
const DefaultSkillPath = "~/.agents/skills"

// Skills is the "skills" section of config.yaml.
//
//	skills:
//	  paths: [~/.agents/skills]   # directories searched for skills
//	  active: [voice, prune]      # skills loaded into the agents
//
// An active entry is a skill name, found in the first path that has it, or a
// path to a skill directory. Discovering a skill never loads it.
type Skills struct {
	Paths  []string
	Active []string
}

type skillsDoc struct {
	Skills *struct {
		Paths  *[]string `yaml:"paths"`
		Active []string  `yaml:"active"`
	} `yaml:"skills"`
}

// LoadSkills reads the skills section of config.yaml in dir. A missing file,
// section or "paths" key gives DefaultSkillPath.
func LoadSkills(dir string) (Skills, error) {
	path := filepath.Join(dir, BaseFile)
	data, _, _, err := readOptional(path)
	if err != nil {
		return Skills{}, err
	}
	return parseSkills(path, data)
}

func parseSkills(source string, data []byte) (Skills, error) {
	var doc skillsDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Skills{}, fmt.Errorf("%s: %w", source, err)
	}
	out := Skills{Paths: []string{DefaultSkillPath}}
	if doc.Skills == nil {
		return out, nil
	}
	if doc.Skills.Paths != nil {
		out.Paths = *doc.Skills.Paths
	}
	out.Active = doc.Skills.Active
	for _, l := range [][]string{out.Paths, out.Active} {
		for _, v := range l {
			if v == "" {
				return Skills{}, fmt.Errorf("%s: skills has an empty entry", source)
			}
		}
	}
	return out, nil
}

// SaveSkills writes the skills section of config.yaml in dir. Everything else
// in the file, including comments, is kept.
func SaveSkills(dir string, s Skills) error {
	path := filepath.Join(dir, BaseFile)
	data, mode, _, err := readOptional(path)
	if err != nil {
		return err
	}
	out, err := editRoot(data, func(root *yaml.Node) error {
		sk := findKey(root, "skills")
		switch {
		case sk == nil:
			sk = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			root.Content = append(root.Content, scalar("skills"), sk)
		case isNull(sk) || (sk.Kind == yaml.MappingNode && len(sk.Content) == 0):
			*sk = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		case sk.Kind != yaml.MappingNode:
			return errors.New("skills must be a mapping")
		}
		setList(sk, "paths", s.Paths)
		setList(sk, "active", s.Active)
		return nil
	})
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if _, err := parseSkills(path, out); err != nil {
		return fmt.Errorf("refusing to write %s: %w", path, err)
	}
	return writeAtomic(path, out, mode)
}

// setList sets key in the mapping m to a list, keeping the key's comments and
// its position when it already exists.
func setList(m *yaml.Node, key string, vals []string) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, v := range vals {
		seq.Content = append(seq.Content, str(v))
	}
	if len(vals) == 0 {
		seq.Style = yaml.FlowStyle
	}
	if i := keyIndex(m, key); i >= 0 {
		old := m.Content[i+1]
		seq.HeadComment, seq.LineComment, seq.FootComment = old.HeadComment, old.LineComment, old.FootComment
		m.Content[i+1] = seq
		return
	}
	m.Content = append(m.Content, scalar(key), seq)
}
