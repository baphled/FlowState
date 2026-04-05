package hook

import (
	"context"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
)

// SkillAutoLoaderHook creates a hook that injects lean skill names into the system prompt.
//
// Expected:
//   - config is a non-nil SkillAutoLoaderConfig.
//   - manifestGetter is called per-request to get the current agent manifest.
//   - bakedSkillNames is the union of app-level and agent-level always-active skill names
//     already injected into the system prompt via BuildSystemPrompt. When non-nil, any
//     skill in this set is stripped from the lean injection to avoid duplication.
//     Pass nil to disable deduplication (backwards compatible).
//
// Returns:
//   - A Hook that prepends "Your load_skills: [...]" to the system message on the first
//     user message only.
//
// Side effects:
//   - Mutates the ChatRequest system message on first invocation.
//   - Passes through without mutation on continuation messages (assistant reply present),
//     tool-call follow-ups (load_skills already injected), or when skill selection yields
//     no skills (empty baseline, no agent skills, no keyword matches).
func SkillAutoLoaderHook(config *SkillAutoLoaderConfig, manifestGetter func() agent.Manifest, bakedSkillNames []string) Hook {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			if containsAssistantMessage(req.Messages) {
				return next(ctx, req)
			}
			manifest := manifestGetter()
			userPrompt := extractUserMessage(req.Messages)
			input := SkillSelectionInput{
				AgentID:            manifest.ID,
				Category:           manifest.Complexity,
				Prompt:             userPrompt,
				AgentDefaultSkills: manifest.Capabilities.AlwaysActiveSkills,
			}
			selection := SelectSkills(input, config)
			if len(selection.Skills) == 0 {
				return next(ctx, req)
			}
			remaining := stripBakedSkills(selection.Skills, bakedSkillNames)
			if len(remaining) == 0 {
				return next(ctx, req)
			}
			lean := buildLeanInjection(remaining)
			injectLeanSkills(req, lean)
			return next(ctx, req)
		}
	}
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
