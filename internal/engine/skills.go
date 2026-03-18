package engine

import (
	"github.com/baphled/flowstate/internal/skill"
)

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
	for _, s := range skills {
		if nameSet[s.Name] {
			filtered = append(filtered, s)
		}
	}
	return filtered
}
