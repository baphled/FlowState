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
func NewLLMCritic(enabled bool, model string) *LLMCritic {
	return &LLMCritic{enabled: enabled, model: model}
}

// Review reviews a plan using the LLM.
func (c *LLMCritic) Review(ctx context.Context, planText string, llmProvider provider.Provider) (*CriticResult, error) {
	if !c.enabled {
		return nil, nil //nolint:nilnil // disabled critic returns no result
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

func parseVerdict(line string, result *CriticResult) {
	v := strings.TrimSpace(strings.TrimPrefix(line, "VERDICT:"))
	if strings.Contains(strings.ToUpper(v), "FAIL") {
		result.Verdict = VerdictFail
	}
}

func parseConfidenceField(line string, result *CriticResult) {
	conf := strings.TrimSpace(strings.TrimPrefix(line, "CONFIDENCE:"))
	if conf != "" {
		if val, err := parseConfidence(conf); err == nil {
			result.Confidence = val
		}
	}
}

func appendToSection(section, line string, result *CriticResult) {
	switch section {
	case "issues":
		result.Issues = append(result.Issues, line)
	case "suggestions":
		result.Suggestions = append(result.Suggestions, line)
	}
}

func parseConfidence(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}
