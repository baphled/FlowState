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
//
// Returns:
//   - A Hook that prepends "Your load_skills: [...]" to the system message on the first
//     user message only.
//
// Side effects:
//   - Mutates the ChatRequest system message on first invocation.
//   - Passes through without mutation on continuation messages (assistant reply present)
//     or tool-call follow-ups (load_skills already injected).
func SkillAutoLoaderHook(config *SkillAutoLoaderConfig, manifestGetter func() agent.Manifest) Hook {
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
			lean := buildLeanInjection(selection.Skills)
			injectLeanSkills(req, lean)
			return next(ctx, req)
		}
	}
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
//   - A formatted string: "Your load_skills: [X, Y]. Call skill_load(name) for each before starting work."
//
// Side effects:
//   - None.
func buildLeanInjection(skills []string) string {
	return fmt.Sprintf("Your load_skills: [%s]. Call skill_load(name) for each before starting work.", strings.Join(skills, ", "))
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
	if len(req.Messages) > 0 && req.Messages[0].Role == "system" && strings.Contains(req.Messages[0].Content, "Your load_skills:") {
		return
	}
	if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
		systemMsg := provider.Message{Role: "system", Content: lean}
		req.Messages = append([]provider.Message{systemMsg}, req.Messages...)
		return
	}
	req.Messages[0].Content = lean + "\n\n" + req.Messages[0].Content
}
