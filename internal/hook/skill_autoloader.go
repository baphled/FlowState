package hook

import (
	"context"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
)

// SkillAutoLoaderHook creates a hook that injects skill content into the system prompt.
//
// When cache is non-nil, skill content is injected directly as XML-style <skill> blocks,
// respecting MaxAutoSkillsBytes for non-baseline skills. When cache is nil, the hook
// falls back to the lean "Your load_skills: [...]" injection format.
//
// Expected:
//   - config is a non-nil SkillAutoLoaderConfig.
//   - manifestGetter is called per-request to get the current agent manifest.
//   - bakedSkillNames is the union of app-level and agent-level always-active skill names
//     already injected into the system prompt via BuildSystemPrompt. When non-nil, any
//     skill in this set is stripped from the lean injection to avoid duplication.
//     Pass nil to disable deduplication (backwards compatible).
//   - cache is an optional pre-initialised SkillContentCache. When non-nil, skill content
//     is injected directly instead of lean names. Pass nil for lean injection fallback.
//
// Returns:
//   - A Hook that injects skill content or lean names into the system message on the
//     first user message only.
//
// Side effects:
//   - Mutates the ChatRequest system message on first invocation.
//   - Passes through without mutation on continuation messages (assistant reply present),
//     tool-call follow-ups (load_skills already injected), or when skill selection yields
//     no skills (empty baseline, no agent skills, no keyword matches).
func SkillAutoLoaderHook(
	config *SkillAutoLoaderConfig,
	manifestGetter func() agent.Manifest,
	bakedSkillNames []string,
	cache *SkillContentCache,
) Hook {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			if containsAssistantMessage(req.Messages) {
				if config.SkipOnSessionContinue {
					injectBaselineOnly(req, config.BaselineSkills, bakedSkillNames)
				}
				return next(ctx, req)
			}
			selection := selectSkillsFromManifest(manifestGetter, req, config)
			if len(selection.Skills) == 0 {
				return next(ctx, req)
			}
			injectSelectedSkills(req, selection.Skills, config, cache, bakedSkillNames)
			return next(ctx, req)
		}
	}
}

// selectSkillsFromManifest builds a SkillSelectionInput from the current manifest and
// request, then runs the three-tier selection algorithm.
//
// Expected:
//   - manifestGetter returns the current agent manifest.
//   - req contains at least one user message.
//   - config is a non-nil SkillAutoLoaderConfig.
//
// Returns:
//   - The SkillSelection result from SelectSkills.
//
// Side effects:
//   - None.
func selectSkillsFromManifest(
	manifestGetter func() agent.Manifest,
	req *provider.ChatRequest,
	config *SkillAutoLoaderConfig,
) SkillSelection {
	manifest := manifestGetter()
	userPrompt := extractUserMessage(req.Messages)
	input := SkillSelectionInput{
		AgentID:            manifest.ID,
		Category:           manifest.Complexity,
		Prompt:             userPrompt,
		AgentDefaultSkills: manifest.Capabilities.AlwaysActiveSkills,
	}
	return SelectSkills(input, config)
}

// injectSelectedSkills chooses between content block injection (when cache is non-nil) and
// lean name injection (when cache is nil), then mutates the request's system message.
//
// Expected:
//   - req is a non-nil ChatRequest.
//   - skills is a non-empty slice of selected skill names.
//   - config is a non-nil SkillAutoLoaderConfig.
//   - cache is an optional SkillContentCache (nil for lean fallback).
//   - bakedSkillNames is the set of skills already in the system prompt (may be nil).
//
// Returns:
//   - None.
//
// Side effects:
//   - Mutates the system message in req.
func injectSelectedSkills(
	req *provider.ChatRequest,
	skills []string,
	config *SkillAutoLoaderConfig,
	cache *SkillContentCache,
	bakedSkillNames []string,
) {
	if cache != nil {
		baselineSet := make(map[string]bool, len(config.BaselineSkills))
		for _, s := range config.BaselineSkills {
			baselineSet[s] = true
		}
		blocks, _ := buildSkillContentBlocks(skills, cache, config.MaxAutoSkillsBytes, baselineSet)
		if blocks != "" {
			injectLeanSkills(req, blocks)
		}
		return
	}
	remaining := stripBakedSkills(skills, bakedSkillNames)
	if len(remaining) == 0 {
		return
	}
	lean := buildLeanInjection(remaining)
	injectLeanSkills(req, lean)
}

// injectBaselineOnly injects only baseline skills into the system message, stripping any
// that are already baked into the prompt.
//
// Expected:
//   - req is a non-nil ChatRequest.
//   - baselineSkills is the list of Tier 1 skill names from the config.
//   - bakedSkillNames is the set of skills already present in the system prompt (may be nil).
//
// Returns:
//   - None.
//
// Side effects:
//   - Mutates the system message in req when baseline skills remain after stripping baked names.
//   - No-ops when all baseline skills are already baked or the baseline list is empty.
func injectBaselineOnly(req *provider.ChatRequest, baselineSkills []string, bakedSkillNames []string) {
	remaining := stripBakedSkills(baselineSkills, bakedSkillNames)
	if len(remaining) == 0 {
		return
	}
	lean := buildLeanInjection(remaining)
	injectLeanSkills(req, lean)
}

// buildSkillContentBlocks formats skill content from the cache into XML-style blocks.
// It enforces ceiling for non-baseline skills, returning injected content and dropped skill names.
//
// Expected:
//   - skills is the ordered list of skill names to inject.
//   - cache is a non-nil, initialised SkillContentCache.
//   - ceiling is the maximum total bytes for non-baseline skill content (0 = no limit).
//   - baselineSet contains skill names that are exempt from byte-budget enforcement.
//
// Returns:
//   - The concatenated skill block content string.
//   - A slice of skill names that were dropped due to ceiling enforcement.
//
// Side effects:
//   - None.
func buildSkillContentBlocks(skills []string, cache *SkillContentCache, ceiling int, baselineSet map[string]bool) (string, []string) {
	var sb strings.Builder
	var dropped []string
	var bytesUsed int
	for _, name := range skills {
		content, ok := cache.GetContent(name)
		if !ok {
			continue
		}
		isBaseline := baselineSet[name]
		if !isBaseline && ceiling > 0 && bytesUsed+len(content) > ceiling {
			dropped = append(dropped, name)
			continue
		}
		fmt.Fprintf(&sb, "<skill name=%q>\n%s\n</skill>\n", name, content)
		if !isBaseline {
			bytesUsed += len(content)
		}
	}
	return sb.String(), dropped
}

// stripBakedSkills returns a filtered copy of skills, removing any name that appears
// in bakedNames. When bakedNames is nil or empty the original slice is returned unchanged.
//
// Expected:
//   - skills is the full selected skill list.
//   - bakedNames is the pre-computed set of skills already baked into BuildSystemPrompt.
//
// Returns:
//   - A slice of skill names not present in bakedNames.
//
// Side effects:
//   - None.
func stripBakedSkills(skills []string, bakedNames []string) []string {
	if len(bakedNames) == 0 {
		return skills
	}
	bakedSet := make(map[string]bool, len(bakedNames))
	for _, name := range bakedNames {
		bakedSet[name] = true
	}
	var remaining []string
	for _, name := range skills {
		if !bakedSet[name] {
			remaining = append(remaining, name)
		}
	}
	return remaining
}

// containsAssistantMessage checks whether any message in the slice has the assistant role.
//
// Expected:
//   - messages is a slice of provider messages (may be empty).
//
// Returns:
//   - true if at least one message has Role == "assistant".
//   - false otherwise.
//
// Side effects:
//   - None.
func containsAssistantMessage(messages []provider.Message) bool {
	for i := range messages {
		if messages[i].Role == "assistant" {
			return true
		}
	}
	return false
}

// buildLeanInjection formats a slice of skill names into the lean injection string.
//
// Expected:
//   - skills is a slice of skill names (may be empty).
//
// Returns:
//   - A formatted string: "Your load_skills: [X, Y]. Use skill_load(name) only when relevant to the current task."
//
// Side effects:
//   - None.
func buildLeanInjection(skills []string) string {
	return fmt.Sprintf("Your load_skills: [%s]. Use skill_load(name) only when relevant to the current task.", strings.Join(skills, ", "))
}

// injectLeanSkills prepends a lean skill string to the system message in a chat request.
//
// Expected:
//   - req is a non-nil ChatRequest.
//   - lean is the formatted lean injection string.
//
// Returns:
//   - None.
//
// Side effects:
//   - Mutates the first system message, or prepends a new system message if none exists.
//   - No-ops when the system message already contains a load_skills directive.
func injectLeanSkills(req *provider.ChatRequest, lean string) {
	if len(req.Messages) > 0 && req.Messages[0].Role == "system" && strings.Contains(req.Messages[0].Content, "Your load_skills: [") {
		return
	}
	if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
		systemMsg := provider.Message{Role: "system", Content: lean}
		req.Messages = append([]provider.Message{systemMsg}, req.Messages...)
		return
	}
	req.Messages[0].Content = lean + "\n\n" + req.Messages[0].Content
}
