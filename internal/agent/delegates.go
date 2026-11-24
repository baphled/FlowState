package agent

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// delegateList reads the delegates sequence of the profile called self whose
// tools are tools. Absent or null is the empty list. A non-empty list needs
// DelegateTool in tools; every entry must be a string naming a profile, with no
// duplicate and not self; at most MaxDelegates entries.
func delegateList(n *yaml.Node, self string, tools []string) ([]string, error) {
	if n == nil || n.Tag == "!!null" {
		return []string{}, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%w: delegates must be a list", ErrInvalidProfile)
	}
	if len(n.Content) > MaxDelegates {
		return nil, fmt.Errorf("%w: delegates lists more than %d profiles", ErrInvalidProfile, MaxDelegates)
	}
	if len(n.Content) > 0 && !hasTool(tools, DelegateTool) {
		return nil, fmt.Errorf("%w: delegates needs %s in tools", ErrInvalidProfile, DelegateTool)
	}
	out := make([]string, 0, len(n.Content))
	seen := make(map[string]bool, len(n.Content))
	for _, item := range n.Content {
		name, err := delegateName(item)
		if err != nil {
			return nil, err
		}
		if name == self {
			return nil, fmt.Errorf("%w: delegates lists the profile itself (%q)", ErrInvalidProfile, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("%w: delegates lists %q twice", ErrInvalidProfile, name)
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

// delegateName reads one delegates entry: a string matching the profile-name rule.
func delegateName(item *yaml.Node) (string, error) {
	name, err := text(item, "a delegates entry")
	if err != nil {
		return "", err
	}
	if item.Tag == "!!null" {
		return "", fmt.Errorf("%w: a delegates entry must be a string", ErrInvalidProfile)
	}
	if !nameRE.MatchString(name) {
		return "", fmt.Errorf("%w: delegates entry %+q must match %s", ErrInvalidProfile, name, nameRE)
	}
	return name, nil
}

// hasTool reports whether tools holds name.
func hasTool(tools []string, name string) bool {
	for _, t := range tools {
		if t == name {
			return true
		}
	}
	return false
}

// CheckDelegates refuses a profile whose delegates list names a profile that
// is not in profiles, naming both. It is the cross-profile rule, run by the
// loader for the scope it loaded so a missing delegate fails early, not at
// spawn. It also re-checks the self-reference and MaxDelegates rules, so a
// hand-built set is held to what Parse enforces. Cycles (a delegates b, b delegates a) are allowed: the delegation
// depth cap bounds the recursion.
func CheckDelegates(profiles []Profile) error {
	loaded := make(map[string]bool, len(profiles))
	for _, p := range profiles {
		loaded[p.Name] = true
	}
	for _, p := range profiles {
		if len(p.Delegates) > MaxDelegates {
			return fmt.Errorf("%w: profile %q delegates lists more than %d profiles", ErrInvalidProfile, p.Name, MaxDelegates)
		}
		for _, d := range p.Delegates {
			if d == p.Name {
				return fmt.Errorf("%w: profile %q delegates lists the profile itself", ErrInvalidProfile, p.Name)
			}
			if !loaded[d] {
				return fmt.Errorf("%w: profile %q delegates to %q, which is not a loaded profile",
					ErrInvalidProfile, p.Name, d)
			}
		}
	}
	return nil
}
