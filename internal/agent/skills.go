package agent

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxSkillNameChars is the longest skill name and the longest prefix, as the skill loader caps a name.
const maxSkillNameChars = 64

// skillNameRE is the loader's rule for a skill name (skill.ValidName in internal/skill, repeated here because
// this adapter imports only the standard library and YAML): lowercase words joined by single hyphens.
var skillNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// skillAlphabetRE is the character set of a skill name, in which a prefix may stop anywhere (`code-r*`).
var skillAlphabetRE = regexp.MustCompile(`^[a-z0-9-]+$`)

// skillsList reads the skills sequence of a profile (ADR-013 D6): the skill names, or `*`-suffixed prefixes, it
// is offered. Absent or null is nil, which offers nothing. A non-empty list needs SkillLoadTool in
// tools, as delegates needs delegate; every entry is a string that is a valid skill name, or such a name cut
// short and followed by `*` (`*` alone: every skill), with no duplicate; at most MaxProfileSkills entries. The
// alphabet admits no path, space or dot form. Whether a name or prefix matches a loaded skill is checked where
// the loaded set is known (the run, `agents validate`), never here: parsing knows no skills directory.
func skillsList(n *yaml.Node, tools []string) ([]string, error) {
	if n == nil || n.Tag == "!!null" {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%w: skills must be a list", ErrInvalidProfile)
	}
	if len(n.Content) > MaxProfileSkills {
		return nil, fmt.Errorf("%w: skills lists more than %d entries", ErrInvalidProfile, MaxProfileSkills)
	}
	if len(n.Content) > 0 && !hasTool(tools, SkillLoadTool) {
		return nil, fmt.Errorf("%w: skills needs %s in tools", ErrInvalidProfile, SkillLoadTool)
	}
	out := make([]string, 0, len(n.Content))
	seen := make(map[string]bool, len(n.Content))
	for _, item := range n.Content {
		entry, err := skillEntry(item)
		if err != nil {
			return nil, err
		}
		if seen[entry] {
			return nil, fmt.Errorf("%w: skills lists %q twice", ErrInvalidProfile, entry)
		}
		seen[entry] = true
		out = append(out, entry)
	}
	return out, nil
}

// skillEntry reads one skills entry: a string naming a skill, or a prefix of one followed by `*`.
func skillEntry(item *yaml.Node) (string, error) {
	entry, err := text(item, "a skills entry")
	if err != nil {
		return "", err
	}
	if item.Tag == "!!null" || !validSkillEntry(entry) {
		return "", fmt.Errorf("%w: a skills entry must be a string that is a skill name or a prefix followed by *: %+q",
			ErrInvalidProfile, entry)
	}
	return entry, nil
}

// validSkillEntry reports whether entry is a skill name, or a prefix of one followed by `*`.
func validSkillEntry(entry string) bool {
	if prefix, ok := strings.CutSuffix(entry, "*"); ok {
		return prefix == "" || len(prefix) <= maxSkillNameChars && skillAlphabetRE.MatchString(prefix)
	}
	return len(entry) <= maxSkillNameChars && skillNameRE.MatchString(entry)
}

// requiresList reads the requires_skills sequence of a profile (ADR-013 D10): the skills, by exact name, its
// sessions must load before any tool but skill.load runs. Absent, null or empty is nil, which requires nothing.
// A non-empty list needs SkillLoadTool in tools, as skills does; at most MaxRequiredSkills entries; every entry a
// valid skill name with no `*` (patterns are for the offer list), no duplicate, and one the profile's own skills
// list offers, exactly or through a prefix: a requirement the model cannot satisfy is a validation error naming it.
func requiresList(n *yaml.Node, tools, skills []string) ([]string, error) {
	if n == nil || n.Tag == "!!null" {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%w: requires_skills must be a list", ErrInvalidProfile)
	}
	if len(n.Content) > MaxRequiredSkills {
		return nil, fmt.Errorf("%w: requires_skills lists more than %d entries", ErrInvalidProfile, MaxRequiredSkills)
	}
	if len(n.Content) == 0 {
		return nil, nil
	}
	if !hasTool(tools, SkillLoadTool) {
		return nil, fmt.Errorf("%w: requires_skills needs %s in tools", ErrInvalidProfile, SkillLoadTool)
	}
	out := make([]string, 0, len(n.Content))
	seen := make(map[string]bool, len(n.Content))
	for _, item := range n.Content {
		name, err := text(item, "a requires_skills entry")
		if err != nil {
			return nil, err
		}
		if item.Tag == "!!null" || !validSkillName(name) {
			return nil, fmt.Errorf("%w: a requires_skills entry must be a string that is an exact skill name: %+q", ErrInvalidProfile, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("%w: requires_skills lists %q twice", ErrInvalidProfile, name)
		}
		if !offers(skills, name) {
			return nil, fmt.Errorf("%w: requires_skills lists %q, which the profile's skills do not offer", ErrInvalidProfile, name)
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

// validSkillName reports whether name is a skill name: the loader's rule, at most maxSkillNameChars, no star.
func validSkillName(name string) bool {
	return len(name) <= maxSkillNameChars && skillNameRE.MatchString(name)
}

// offers reports whether an offer list offers name: through an exact entry, or a `*` entry whose text before the
// star is a prefix of it (a lone star offers every skill).
func offers(entries []string, name string) bool {
	for _, e := range entries {
		if prefix, ok := strings.CutSuffix(e, "*"); ok {
			if prefix == "" || strings.HasPrefix(name, prefix) {
				return true
			}
			continue
		}
		if e == name {
			return true
		}
	}
	return false
}
