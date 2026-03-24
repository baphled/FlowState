package plan

import (
	"context"
	"strconv"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
)

// CriticVerdict represents the verdict from the LLM critic.
type CriticVerdict string

const (
	// VerdictPass indicates the plan passed review.
	VerdictPass CriticVerdict = "PASS"
	// VerdictFail indicates the plan failed review.
	VerdictFail CriticVerdict = "FAIL"
	// VerdictDisabled indicates the critic is disabled.
	VerdictDisabled CriticVerdict = "DISABLED"
)

// CriticResult holds the result of a plan review.
type CriticResult struct {
	Verdict     CriticVerdict
	Issues      []string
	Suggestions []string
	Confidence  float64
}

// LLMCritic reviews plans using an LLM.
type LLMCritic struct {
	enabled bool
	model   string
}

// NewLLMCritic creates a new LLMCritic.
//
// Expected:
//   - model is a valid LLM model identifier when enabled is true.
//
// Returns:
//   - A configured LLMCritic instance.
//
// Side effects:
//   - None.
func NewLLMCritic(enabled bool, model string) *LLMCritic {
	return &LLMCritic{enabled: enabled, model: model}
}

// Review reviews a plan using the LLM.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - planText contains the plan to review.
//   - llmProvider is a valid provider for chat completions.
//
// Returns:
//   - A CriticResult with verdict, issues, suggestions, and confidence.
//   - An error if the LLM call fails.
//
// Side effects:
//   - Sends a chat request to the LLM provider.
func (c *LLMCritic) Review(ctx context.Context, planText string, llmProvider provider.Provider) (*CriticResult, error) {
	if !c.enabled {
		return &CriticResult{
			Verdict:    VerdictDisabled,
			Confidence: 1.0,
		}, nil
	}

	systemPrompt := `You are a plan quality reviewer. Review the following plan and respond with:
VERDICT: PASS or FAIL
ISSUES: (list any issues, one per line, or "none")
SUGGESTIONS: (list suggestions, one per line, or "none")
CONFIDENCE: (0.0 to 1.0)`

	req := provider.ChatRequest{
		Model: c.model,
		Messages: []provider.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: planText},
		},
	}

	resp, err := llmProvider.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	return parseCriticResponse(resp.Message.Content), nil
}

// parseCriticResponse parses the structured text response from the LLM critic into a CriticResult.
//
// Expected:
//   - content contains a structured response with VERDICT:, ISSUES:, SUGGESTIONS:, and CONFIDENCE: fields.
//
// Returns:
//   - A CriticResult populated from the parsed fields.
//
// Side effects:
//   - None.
func parseCriticResponse(content string) *CriticResult {
	result := &CriticResult{
		Verdict:    VerdictPass,
		Confidence: 0.8,
	}

	lines := strings.Split(content, "\n")
	section := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch {
		case strings.HasPrefix(line, "VERDICT:"):
			parseVerdict(line, result)
			section = ""
		case strings.HasPrefix(line, "ISSUES:"):
			section = "issues"
		case strings.HasPrefix(line, "SUGGESTIONS:"):
			section = "suggestions"
		case strings.HasPrefix(line, "CONFIDENCE:"):
			parseConfidenceField(line, result)
			section = "confidence"
		case line != "none":
			appendToSection(section, line, result)
		}
	}

	if result.Verdict == VerdictFail && len(result.Issues) == 0 {
		result.Issues = []string{"Unspecified failure"}
	}
	return result
}

// parseVerdict extracts the verdict from a VERDICT: line and sets it on the result.
//
// Expected:
//   - line starts with "VERDICT:" followed by PASS or FAIL.
//
// Side effects:
//   - Mutates result.Verdict to VerdictFail if the line contains "FAIL".
func parseVerdict(line string, result *CriticResult) {
	v := strings.TrimSpace(strings.TrimPrefix(line, "VERDICT:"))
	if strings.Contains(strings.ToUpper(v), "FAIL") {
		result.Verdict = VerdictFail
	}
}

// parseConfidenceField extracts the confidence value from a CONFIDENCE: line and sets it on the result.
//
// Expected:
//   - line starts with "CONFIDENCE:" followed by a float value.
//
// Side effects:
//   - Mutates result.Confidence if a valid float is parsed.
func parseConfidenceField(line string, result *CriticResult) {
	conf := strings.TrimSpace(strings.TrimPrefix(line, "CONFIDENCE:"))
	if conf != "" {
		if val, err := parseConfidence(conf); err == nil {
			result.Confidence = val
		}
	}
}

// appendToSection appends a line to the appropriate section (issues or suggestions) of the result.
//
// Expected:
//   - section is one of "issues" or "suggestions".
//
// Side effects:
//   - Mutates result.Issues or result.Suggestions by appending the line.
func appendToSection(section, line string, result *CriticResult) {
	switch section {
	case "issues":
		result.Issues = append(result.Issues, line)
	case "suggestions":
		result.Suggestions = append(result.Suggestions, line)
	}
}

// parseConfidence converts a string to a float64 confidence score.
//
// Expected:
//   - s is a string representation of a float64 value.
//
// Returns:
//   - The parsed float64 value.
//   - An error if the string cannot be parsed.
//
// Side effects:
//   - None.
func parseConfidence(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}
