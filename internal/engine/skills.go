package engine

import (
	"github.com/baphled/flowstate/internal/skill"
)

// LoadAlwaysActiveSkills loads skills that should always be active for the given agent.
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
func LoadAlwaysActiveSkills(skillsDir string, appLevel []string, agentLevel []string) []skill.Skill {
	merged := mergeSkillNames(appLevel, agentLevel)
	if len(merged) == 0 {
		return nil
	}

	loader := skill.NewFileSkillLoader(skillsDir)
	allSkills, err := loader.LoadAll()
	if err != nil {
		return nil
	}

	return filterSkillsByName(allSkills, merged)
}

func mergeSkillNames(appLevel []string, agentLevel []string) []string {
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
