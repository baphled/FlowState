package harness

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/baphled/flowstate/internal/plan"
	promptpkg "github.com/baphled/flowstate/internal/prompt"
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

const requiredRubricCount = 6

// CriticResult holds the result of a plan review including per-criterion rubric verdicts.
//
// Expected:
//   - Verdict is one of VerdictPass, VerdictFail, or VerdictDisabled.
//   - RubricResults contains exactly 6 entries when Verdict is PASS or FAIL.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type CriticResult struct {
	Verdict       CriticVerdict
	Issues        []string
	Suggestions   []string
	Confidence    float64
	RubricResults map[string]string
}

// LLMCritic reviews plans using an LLM against a 6-criterion rubric.
//
// Expected:
//   - enabled is true to run critic, false to return VerdictDisabled.
//   - model is a valid LLM model identifier when enabled is true.
//   - systemPrompt contains the embedded critic prompt loaded at construction.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type LLMCritic struct {
	enabled      bool
	model        string
	systemPrompt string
}

// NewLLMCritic creates a new LLMCritic with its system prompt loaded from the embedded prompt filesystem.
//
// Expected:
//   - model is a valid LLM model identifier when enabled is true.
//
// Returns:
//   - A configured LLMCritic instance and nil error, or nil and an error if the prompt cannot be loaded.
//
// Side effects:
//   - None.
func NewLLMCritic(enabled bool, model string) (*LLMCritic, error) {
	prompt, err := promptpkg.GetPrompt("harness_critic")
	if err != nil {
		return nil, fmt.Errorf("loading critic prompt: %w", err)
	}
	return &LLMCritic{enabled: enabled, model: model, systemPrompt: prompt}, nil
}

// Review reviews a plan using the LLM against a 6-criterion rubric.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - planData is the structured plan (may be nil if parsing failed).
//   - planText is the raw plan text preserving rationale and risks.
//   - validatorResult is the Go validator outcome (may be nil).
//   - llmProvider is a valid provider for chat completions.
//
// Returns:
//   - A CriticResult with verdict, rubric results, issues, suggestions, and confidence.
//   - An error if the LLM call fails or the response is malformed.
//
// Side effects:
//   - Sends a chat request to the LLM provider when enabled.
func (c *LLMCritic) Review(
	ctx context.Context,
	planData *plan.File,
	planText string,
	validatorResult *plan.ValidationResult,
	llmProvider provider.Provider,
) (*CriticResult, error) {
	if !c.enabled {
		return &CriticResult{
			Verdict:    VerdictDisabled,
			Confidence: 1.0,
		}, nil
	}

	userContent := buildCriticUserMessage(planData, planText, validatorResult)

	req := provider.ChatRequest{
		Model: c.model,
		Messages: []provider.Message{
			{Role: "system", Content: c.systemPrompt},
			{Role: "user", Content: userContent},
		},
	}

	resp, err := llmProvider.Chat(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("critic chat request: %w", err)
	}

	return parseCriticResponse(resp.Message.Content)
}

// buildCriticUserMessage assembles the user message for the critic from plan data and validator results.
//
// Expected:
//   - planText is non-empty raw plan text.
//   - plan and validatorResult may be nil.
//
// Returns:
//   - A formatted string containing plan text and validator summary.
//
// Side effects:
//   - None.
func buildCriticUserMessage(planData *plan.File, planText string, validatorResult *plan.ValidationResult) string {
	var b strings.Builder
	b.WriteString("## Plan Text\n\n")
	b.WriteString(planText)

	if validatorResult != nil {
		b.WriteString("\n\n## Validator Results\n\n")
		fmt.Fprintf(&b, "Valid: %t, Score: %.2f\n", validatorResult.Valid, validatorResult.Score)
		if len(validatorResult.Errors) > 0 {
			b.WriteString("Errors:\n")
			for _, e := range validatorResult.Errors {
				b.WriteString("- ")
				b.WriteString(e)
				b.WriteString("\n")
			}
		}
	}

	if planData != nil {
		fmt.Fprintf(&b, "\n\n## Plan Metadata\n\nID: %s\nTitle: %s\nTasks: %d\n", planData.ID, planData.Title, len(planData.Tasks))
	}

	return b.String()
}

// parseCriticResponse parses the structured text response from the LLM critic into a CriticResult.
//
// Expected:
//   - content is a structured response with VERDICT:, CONFIDENCE:, RUBRIC:, ISSUES:, and SUGGESTIONS: fields.
//
// Returns:
//   - A CriticResult populated from the parsed fields.
//   - An error if the response is missing required fields or is malformed.
//
// Side effects:
//   - None.
func parseCriticResponse(content string) (*CriticResult, error) {
	if strings.TrimSpace(content) == "" {
		return nil, errors.New("critic response missing VERDICT")
	}

	result := &CriticResult{
		Verdict:       "",
		Confidence:    0.0,
		RubricResults: map[string]string{},
	}

	tracker := &parseTracker{}
	lines := strings.Split(content, "\n")
	section := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var err error
		section, err = routeCriticLine(trimmed, section, result, tracker)
		if err != nil {
			return nil, err
		}
	}

	return validateCriticParse(tracker, result)
}

// parseTracker records which required sections have been found during parsing.
//
// Expected:
//   - All fields start as false and are set to true as sections are encountered.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type parseTracker struct {
	hasVerdict    bool
	hasConfidence bool
	hasRubric     bool
}

// routeCriticLine routes a single non-empty trimmed line to the appropriate parser.
//
// Expected:
//   - trimmed is a non-empty string from the critic response.
//   - section is the current parsing section name.
//
// Returns:
//   - The updated section name and an error if parsing fails.
//
// Side effects:
//   - Mutates result fields and tracker flags.
func routeCriticLine(trimmed, section string, result *CriticResult, tracker *parseTracker) (string, error) {
	switch {
	case strings.HasPrefix(trimmed, "VERDICT:"):
		if err := parseVerdict(trimmed, result); err != nil {
			return "", err
		}
		tracker.hasVerdict = true
		return "", nil
	case strings.HasPrefix(trimmed, "CONFIDENCE:"):
		if err := parseConfidenceField(trimmed, result); err != nil {
			return "", err
		}
		tracker.hasConfidence = true
		return "", nil
	case strings.HasPrefix(trimmed, "RUBRIC:"):
		tracker.hasRubric = true
		return "rubric", nil
	case strings.HasPrefix(trimmed, "ISSUES:"):
		return "issues", nil
	case strings.HasPrefix(trimmed, "SUGGESTIONS:"):
		return "suggestions", nil
	default:
		appendToSection(section, trimmed, result)
		return section, nil
	}
}

// validateCriticParse checks that all required sections were found during parsing.
//
// Expected:
//   - tracker records which sections were found.
//   - result is the partially populated CriticResult.
//
// Returns:
//   - The validated CriticResult or an error if required sections are missing.
//
// Side effects:
//   - None.
func validateCriticParse(tracker *parseTracker, result *CriticResult) (*CriticResult, error) {
	if !tracker.hasVerdict {
		return nil, errors.New("critic response missing VERDICT")
	}
	if !tracker.hasConfidence {
		return nil, errors.New("critic response missing CONFIDENCE")
	}
	if !tracker.hasRubric {
		return nil, errors.New("critic response missing RUBRIC block")
	}
	if len(result.RubricResults) < requiredRubricCount {
		return nil, fmt.Errorf("critic response rubric has %d entries, need %d", len(result.RubricResults), requiredRubricCount)
	}

	return result, nil
}

// parseVerdict extracts and validates the verdict from a VERDICT: line.
//
// Expected:
//   - line starts with "VERDICT:" followed by PASS or FAIL (case-insensitive).
//
// Returns:
//   - An error if the verdict value is not PASS or FAIL.
//
// Side effects:
//   - Mutates result.Verdict.
func parseVerdict(line string, result *CriticResult) error {
	v := strings.TrimSpace(strings.TrimPrefix(line, "VERDICT:"))
	upper := strings.ToUpper(v)
	switch upper {
	case "PASS":
		result.Verdict = VerdictPass
	case "FAIL":
		result.Verdict = VerdictFail
	default:
		return fmt.Errorf("critic response has invalid verdict %q, expected PASS or FAIL", v)
	}
	return nil
}

// parseConfidenceField extracts and validates the confidence value from a CONFIDENCE: line.
//
// Expected:
//   - line starts with "CONFIDENCE:" followed by a float value.
//
// Returns:
//   - An error if the confidence value is missing or unparseable.
//
// Side effects:
//   - Mutates result.Confidence.
func parseConfidenceField(line string, result *CriticResult) error {
	conf := strings.TrimSpace(strings.TrimPrefix(line, "CONFIDENCE:"))
	if conf == "" {
		return errors.New("critic response missing CONFIDENCE value")
	}
	val, err := strconv.ParseFloat(conf, 64)
	if err != nil {
		return fmt.Errorf("critic response has unparseable CONFIDENCE %q: %w", conf, err)
	}
	result.Confidence = val
	return nil
}

// appendToSection appends a parsed line to the appropriate section of the result.
//
// Expected:
//   - section is one of "rubric", "issues", or "suggestions".
//   - line is a trimmed non-empty string.
//
// Side effects:
//   - Mutates result.Issues, result.Suggestions, or result.RubricResults.
func appendToSection(section, line string, result *CriticResult) {
	cleaned := strings.TrimPrefix(line, "- ")
	switch section {
	case "rubric":
		parseRubricEntry(cleaned, result)
	case "issues":
		if !strings.EqualFold(cleaned, "none") {
			result.Issues = append(result.Issues, cleaned)
		}
	case "suggestions":
		if !strings.EqualFold(cleaned, "none") {
			result.Suggestions = append(result.Suggestions, cleaned)
		}
	}
}

// parseRubricEntry parses a single rubric line like "FEASIBILITY: PASS - description".
//
// Expected:
//   - entry follows the format "CRITERION: PASS|FAIL - description".
//
// Side effects:
//   - Mutates result.RubricResults by adding the criterion and its verdict.
func parseRubricEntry(entry string, result *CriticResult) {
	colonIdx := strings.Index(entry, ":")
	if colonIdx < 0 {
		return
	}
	criterion := strings.TrimSpace(entry[:colonIdx])
	remainder := strings.TrimSpace(entry[colonIdx+1:])
	dashIdx := strings.Index(remainder, "-")
	if dashIdx < 0 {
		return
	}
	verdict := strings.TrimSpace(remainder[:dashIdx])
	upper := strings.ToUpper(verdict)
	if upper == "PASS" || upper == "FAIL" {
		result.RubricResults[criterion] = upper
	}
}
