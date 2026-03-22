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
//   - A Hook that prepends "Your load_skills: [...]" to the system message.
//
// Side effects:
//   - Mutates the ChatRequest system message.
func SkillAutoLoaderHook(config *SkillAutoLoaderConfig, manifestGetter func() agent.Manifest) Hook {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
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
			setSkillMetadata(req, selection.Skills)
			return next(ctx, req)
		}
	}
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

// setSkillMetadata stores the selected skill names in the request metadata for downstream consumers.
//
// Expected:
//   - req is a non-nil ChatRequest.
//   - skills is a slice of skill names (may be empty).
//
// Side effects:
//   - Initialises req.Metadata if nil, then sets the "loaded_skills" key.
func setSkillMetadata(req *provider.ChatRequest, skills []string) {
	if req.Metadata == nil {
		req.Metadata = make(map[string]any)
	}
	req.Metadata["loaded_skills"] = skills
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
func injectLeanSkills(req *provider.ChatRequest, lean string) {
	if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
		systemMsg := provider.Message{Role: "system", Content: lean}
		req.Messages = append([]provider.Message{systemMsg}, req.Messages...)
		return
	}
	req.Messages[0].Content = lean + "\n\n" + req.Messages[0].Content
}
