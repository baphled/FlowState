package hook

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
)

// SkillAutoLoaderHook creates a hook that injects skill content into the system prompt.
//
// When cache is non-nil, skill content is injected directly as XML-style <skill> blocks,
// respecting MaxAutoSkillsBytes for non-baseline skills. When cache is nil, the hook
// falls back to the lean <available_skills> system-reminder block (Agent Runtime
// Quality plan, Item 2 — May 2026).
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
			selection := selectSkillsFromManifest(manifestGetter, req, config, cache)
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
//   - cache is an optional SkillContentCache for byte-budget enforcement in SelectSkills.
//     When non-nil, SelectSkills applies PerSkillMaxBytes and MaxAutoSkillsBytes filtering.
//     Pass nil to use count-only selection.
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
	cache *SkillContentCache,
) SkillSelection {
	manifest := manifestGetter()
	userPrompt := extractUserMessage(req.Messages)
	input := SkillSelectionInput{
		AgentID:            manifest.ID,
		Category:           manifest.Complexity,
		Prompt:             userPrompt,
		AgentDefaultSkills: manifest.Capabilities.AlwaysActiveSkills,
		Cache:              cache,
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
	baselineSet := make(map[string]bool, len(config.BaselineSkills))
	for _, s := range config.BaselineSkills {
		baselineSet[s] = true
	}
	var baseline, contextual []string
	for _, s := range skills {
		if baselineSet[s] {
			baseline = append(baseline, s)
		} else {
			contextual = append(contextual, s)
		}
	}
	if cache != nil {
		blocks, _ := buildSkillContentBlocks(skills, cache, config.MaxAutoSkillsBytes, baselineSet)
		lean := buildLeanInjection(baseline, contextual)
		if blocks != "" {
			injectLeanSkills(req, lean+"\n\n"+blocks)
		} else {
			injectLeanSkills(req, lean)
		}
		return
	}
	// Baseline skills are always injected as session-start mandatory; only strip baked skills
	// from the contextual (non-baseline) list.
	contextual = stripBakedSkills(contextual, bakedSkillNames)
	if len(baseline) == 0 && len(contextual) == 0 {
		return
	}
	lean := buildLeanInjection(baseline, contextual)
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
	lean := buildLeanInjection(remaining, nil)
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

// availableSkillsBlockMarker is the substring stable across both the
// session-start and contextual tiers that downstream code uses to
// detect an already-injected block. Kept short so the dedupe check
// stays cheap; the full open tag would be just as correct but more
// expensive to compare on every request.
const availableSkillsBlockMarker = "<available_skills>"

// buildLeanInjection renders the Item 2 <available_skills>
// system-reminder block (Agent Runtime Quality plan, May 2026). The
// pre-D1 prose format ("Your load_skills: session start...; load when
// relevant: [...]") was a known hallucination vector: the model treated
// listed names as inlineable tool calls (`task-tracker(...)`) instead
// of skills to be loaded via skill_load. The XML-style block mirrors
// Claude Code's <system-reminder> + <available_skills> shape, which
// the verbatim anti-hallucination clause refers to ("Available skills
// are listed in system-reminder messages").
//
// sessionStartSkills carry the "must invoke before first response"
// semantic; contextualSkills carry the "load when relevant" semantic.
// Both tiers are surfaced inside the same <available_skills> element
// to keep the block compact, with the always-active marker carried as
// an inline attribute on each skill entry.
//
// Expected:
//   - sessionStartSkills is the baseline (always-active) skill list; may be empty.
//   - contextualSkills is the agent/keyword skill list; may be empty.
//   - At least one tier must be non-empty; an all-empty call returns
//     the empty string so callers can short-circuit injection.
//
// Returns:
//   - A formatted <system-reminder><available_skills>...</available_skills>...</system-reminder>
//     block, or the empty string when both tiers are empty.
//
// Side effects:
//   - None.
func buildLeanInjection(sessionStartSkills, contextualSkills []string) string {
	if len(sessionStartSkills) == 0 && len(contextualSkills) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<system-reminder>\n")
	sb.WriteString("The following skills are available for use with the skill_load tool:\n\n")
	sb.WriteString("<available_skills>\n")
	for _, name := range sessionStartSkills {
		fmt.Fprintf(&sb, "  <skill name=%q tier=\"session-start\" />\n", name)
	}
	for _, name := range contextualSkills {
		fmt.Fprintf(&sb, "  <skill name=%q tier=\"contextual\" />\n", name)
	}
	sb.WriteString("</available_skills>\n\n")
	sb.WriteString("Call skill_load(name=\"<exact-name>\") to invoke. Names are case-sensitive and must match exactly. Skills are NOT tools — do not attempt to call them directly. Only invoke a skill that appears in the <available_skills> list, or one the user explicitly typed as `/<name>` in their message. Never guess or invent a skill name from training data; otherwise do not call this tool.\n")
	sb.WriteString("</system-reminder>")
	return sb.String()
}

// injectLeanSkills prepends a lean skill string to the system message in a chat request.
//
// Expected:
//   - req is a non-nil ChatRequest.
//   - lean is the formatted lean injection string. Empty strings are
//     ignored (callers can pass the empty result from buildLeanInjection
//     and rely on this no-op).
//
// Returns:
//   - None.
//
// Side effects:
//   - Mutates the first system message, or prepends a new system message if none exists.
//   - No-ops when the system message already contains an <available_skills> block.
func injectLeanSkills(req *provider.ChatRequest, lean string) {
	if lean == "" {
		return
	}
	if len(req.Messages) > 0 && req.Messages[0].Role == "system" && strings.Contains(req.Messages[0].Content, availableSkillsBlockMarker) {
		return
	}
	if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
		systemMsg := provider.Message{Role: "system", Content: lean}
		req.Messages = append([]provider.Message{systemMsg}, req.Messages...)
		return
	}
	req.Messages[0].Content = lean + "\n\n" + req.Messages[0].Content
}

// KnownSkills returns the catalogue of skill names the autoloader could
// surface in an <available_skills> block for the given manifest. It is
// the union of:
//
//   - cfg.BaselineSkills (always-available across every request)
//   - manifest.Capabilities.AlwaysActiveSkills (per-agent session-start tier)
//   - every cfg.KeywordPatterns[].Skills entry (contextual tier — any
//     prompt could plausibly trigger one of these)
//   - every cfg.CategoryMappings[*] entry (the category-tier set the
//     selector may surface for a matching category)
//
// The result is the SUPERSET of any single request's
// <available_skills> block — keyword/category tiers only fire when the
// prompt matches, but a skill that appears in any pattern is still a
// "known skill" the model could plausibly hallucinate as a tool name.
// The engine's Item 3 skill-name redirect uses this superset so the
// recovery hint fires consistently across the conversation rather than
// drifting turn-to-turn with the keyword matcher.
//
// Expected:
//   - cfg may be nil; a nil cfg yields only the manifest's always-active
//     skills (no baseline, no patterns).
//   - manifest may be the zero value; an empty manifest yields only
//     cfg-derived names.
//
// Returns:
//   - A sorted, deduplicated slice of skill names. Empty slice when
//     neither source provides any names.
//
// Side effects:
//   - None.
func KnownSkills(cfg *SkillAutoLoaderConfig, manifest agent.Manifest) []string {
	seen := make(map[string]struct{})
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		seen[name] = struct{}{}
	}

	if cfg != nil {
		for _, s := range cfg.BaselineSkills {
			add(s)
		}
		for _, kp := range cfg.KeywordPatterns {
			for _, s := range kp.Skills {
				add(s)
			}
		}
		for _, skills := range cfg.CategoryMappings {
			for _, s := range skills {
				add(s)
			}
		}
	}
	for _, s := range manifest.Capabilities.AlwaysActiveSkills {
		add(s)
	}

	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
