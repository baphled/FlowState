package hook

import "strings"

// SkillSelection represents the result of skill selection.
//
// SkillSelection contains the list of selected skills and their sources,
// indicating where each skill was selected from (config, agent defaults, etc).
//
// Expected:
//   - Skills may be empty for cases with no selected skills.
//   - Sources may be empty if no source tracking is needed.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type SkillSelection struct {
	Skills  []string      `json:"skills"`
	Sources []SkillSource `json:"sources"`
}

// SkillSource represents the source of a selected skill.
//
// SkillSource tracks where a skill was selected from, including the skill name,
// the source (e.g., "config", "agent", "pattern"), and any pattern that matched
// to select this skill.
//
// Expected:
//   - Skill must be non-empty.
//   - Source must be non-empty.
//   - Pattern may be empty if no pattern matching was used.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type SkillSource struct {
	Skill   string `json:"skill"`
	Source  string `json:"source"`
	Pattern string `json:"pattern"`
}

// SkillSelectionInput represents the input parameters for skill selection.
//
// SkillSelectionInput contains all information needed to make a skill selection
// decision, including the agent ID, category, prompt, existing skills, and
// agent default skills.
//
// Expected:
//   - AgentID must be non-empty.
//   - Category may be empty.
//   - Prompt may be empty.
//   - ExistingSkills may be empty.
//   - AgentDefaultSkills may be empty.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type SkillSelectionInput struct {
	AgentID            string   `json:"agentId"`
	Category           string   `json:"category"`
	Prompt             string   `json:"prompt"`
	ExistingSkills     []string `json:"existingSkills"`
	AgentDefaultSkills []string `json:"agentDefaultSkills"`
}

// SelectSkills applies the three-tier skill selection algorithm.
//
// SelectSkills selects skills from baseline, agent defaults, and keyword
// patterns, deduplicating and applying the MaxAutoSkills cap to non-baseline
// skills.
//
// Expected:
//   - input contains the agent context and prompt for skill selection.
//   - config contains baseline skills, keyword patterns, and the auto-skills cap.
//
// Returns:
//   - A SkillSelection with deduplicated skills and their sources.
//
// Side effects:
//   - None.
func SelectSkills(input SkillSelectionInput, config *SkillAutoLoaderConfig) SkillSelection {
	seen := make(map[string]bool)
	var skills []string
	var sources []SkillSource

	for _, skill := range config.BaselineSkills {
		if seen[skill] {
			continue
		}
		seen[skill] = true
		skills = append(skills, skill)
		sources = append(sources, SkillSource{Skill: skill, Source: "baseline", Pattern: ""})
	}

	autoCount := 0

	for _, skill := range input.AgentDefaultSkills {
		if seen[skill] || autoCount >= config.MaxAutoSkills {
			continue
		}
		seen[skill] = true
		skills = append(skills, skill)
		sources = append(sources, SkillSource{Skill: skill, Source: "agent", Pattern: ""})
		autoCount++
	}

	promptLower := strings.ToLower(input.Prompt)
	for _, kp := range config.KeywordPatterns {
		if autoCount >= config.MaxAutoSkills {
			break
		}
		patternLower := strings.ToLower(kp.Pattern)
		if !strings.Contains(promptLower, patternLower) {
			continue
		}
		for _, skill := range kp.Skills {
			if seen[skill] || autoCount >= config.MaxAutoSkills {
				continue
			}
			seen[skill] = true
			skills = append(skills, skill)
			sources = append(sources, SkillSource{Skill: skill, Source: "keyword", Pattern: kp.Pattern})
			autoCount++
		}
	}

	return SkillSelection{Skills: skills, Sources: sources}
}
