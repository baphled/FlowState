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
	// SkillsDropped contains skill names excluded due to byte-budget enforcement.
	//
	// Expected:
	//   - Empty when Cache is nil or no skills exceeded the budget.
	//
	// Returns:
	//   - N/A (struct field).
	//
	// Side effects:
	//   - None.
	SkillsDropped []string `json:"skills_dropped"`
	// BytesUsed is the aggregate byte size of non-baseline skills included.
	//
	// Expected:
	//   - Zero when Cache is nil or no non-baseline skills were selected.
	//
	// Returns:
	//   - N/A (struct field).
	//
	// Side effects:
	//   - None.
	BytesUsed int `json:"bytes_used"`
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
	// Cache is an optional SkillContentCache used for byte-budget enforcement.
	// When nil, byte-budget enforcement is skipped and count-only selection applies.
	//
	// Expected:
	//   - May be nil; when nil, only count-based MaxAutoSkills cap is applied.
	//
	// Returns:
	//   - N/A (struct field).
	//
	// Side effects:
	//   - None.
	Cache *SkillContentCache `json:"-"`
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

	if input.Cache != nil && config.MaxAutoSkillsBytes > 0 {
		return applyByteBudget(skills, sources, config, input.Cache)
	}

	return SkillSelection{Skills: skills, Sources: sources}
}

// applyByteBudget filters non-baseline skills that exceed the configured byte
// budget, keeping baseline skills unconditionally.
//
// Expected:
//   - skills and sources have matching indices and equal lengths.
//   - config contains MaxAutoSkillsBytes and PerSkillMaxBytes thresholds.
//   - cache is non-nil and has been initialised.
//
// Returns:
//   - A SkillSelection with filtered skills, matching sources, dropped names, and aggregate byte usage.
//
// Side effects:
//   - None.
func applyByteBudget(skills []string, sources []SkillSource, config *SkillAutoLoaderConfig, cache *SkillContentCache) SkillSelection {
	nonBaselineStart := len(config.BaselineSkills)
	baselineSet := make(map[string]bool, len(config.BaselineSkills))
	for _, s := range config.BaselineSkills {
		baselineSet[s] = true
	}

	var kept []string
	var keptSources []SkillSource
	var dropped []string
	var bytesUsed int

	for i, skill := range skills {
		if i < nonBaselineStart || baselineSet[skill] {
			kept = append(kept, skill)
			keptSources = append(keptSources, sources[i])
			continue
		}

		size := cache.ByteSize(skill)
		if config.PerSkillMaxBytes > 0 && size > config.PerSkillMaxBytes {
			dropped = append(dropped, skill)
			continue
		}
		if bytesUsed+size > config.MaxAutoSkillsBytes {
			dropped = append(dropped, skill)
			continue
		}

		kept = append(kept, skill)
		keptSources = append(keptSources, sources[i])
		bytesUsed += size
	}

	return SkillSelection{
		Skills:        kept,
		Sources:       keptSources,
		SkillsDropped: dropped,
		BytesUsed:     bytesUsed,
	}
}
