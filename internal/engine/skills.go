package engine

import (
	"log/slog"

	"github.com/baphled/flowstate/internal/skill"
)

// LoadAlwaysActiveSkills loads skills that should always be active for the given agent.
//
// Every requested skill that resolves to no on-disk SKILL.md is skipped
// (boot-time resilience is unchanged — a missing skill never blocks
// startup) but is reported loudly: one WARN per missing name, with the
// name embedded in the message so log consumers and validators can
// surface the silent-drop case the invariant suite pins.
//
// Expected:
//   - skillsDir is the directory path containing skill definitions.
//   - appLevel is the list of application-level always-active skill names.
//   - agentLevel is the list of agent-level always-active skill names.
//
// Returns:
//   - A slice of Skill values matching the merged skill names, or nil on error.
//
// Side effects:
//   - Reads skill files from the skillsDir directory.
//   - Logs one WARN per requested skill missing on disk.
func LoadAlwaysActiveSkills(skillsDir string, appLevel []string, agentLevel []string) []skill.Skill {
	merged := MergeSkillNames(appLevel, agentLevel)
	if len(merged) == 0 {
		return nil
	}

	loader := skill.NewFileSkillLoader(skillsDir)
	allSkills, err := loader.LoadAll()
	if err != nil {
		slog.Warn("always-active skills could not be read from the skills directory",
			"skills_dir", skillsDir,
			"error", err,
		)
		return nil
	}

	loaded := filterSkillsByName(allSkills, merged)
	warnMissingAlwaysActiveSkills(merged, loaded, skillsDir)
	return loaded
}

// warnMissingAlwaysActiveSkills logs one WARN per requested always-active
// skill name that resolved to no loaded skill. The skill name is embedded
// in the message body (not only as an attribute) so message-keyed log
// consumers and validation harnesses can match it directly.
//
// Expected: merged is the requested skill-name list; loaded is the
// resolved skill slice; skillsDir names the directory that was searched.
// Returns: None.
// Side effects: emits one slog.Warn per missing skill name.
func warnMissingAlwaysActiveSkills(merged []string, loaded []skill.Skill, skillsDir string) {
	present := make(map[string]bool, len(loaded))
	for _, s := range loaded {
		present[s.Name] = true
	}
	for _, name := range merged {
		if present[name] {
			continue
		}
		slog.Warn("always-active skill "+name+" not found on disk under the configured skill_dir — the agent will run without it",
			"skill", name,
			"skills_dir", skillsDir,
		)
	}
}

// MergeSkillNames combines application-level and agent-level skill names, removing duplicates.
//
// Expected:
//   - appLevel contains application-level skill names.
//   - agentLevel contains agent-level skill names.
//
// Returns:
//   - A deduplicated slice of skill names, with app-level names appearing first.
//
// Side effects:
//   - None.
func MergeSkillNames(appLevel []string, agentLevel []string) []string {
	seen := make(map[string]bool)
	var result []string

	for _, name := range appLevel {
		if name != "" && !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}

	for _, name := range agentLevel {
		if name != "" && !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}

	return result
}

// filterSkillsByName returns only the skills whose names appear in the provided list.
//
// Expected:
//   - skills is a slice of skill.Skill values.
//   - names is a slice of skill names to match.
//
// Returns:
//   - A slice of skills matching the provided names, in the order they appear in the input slice.
//
// Side effects:
//   - None.
func filterSkillsByName(skills []skill.Skill, names []string) []skill.Skill {
	nameSet := make(map[string]bool)
	for _, name := range names {
		nameSet[name] = true
	}

	var filtered []skill.Skill
	for i := range skills {
		if nameSet[skills[i].Name] {
			filtered = append(filtered, skills[i])
		}
	}
	return filtered
}
