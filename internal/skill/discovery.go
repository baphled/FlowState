// Package skill provides skill loading, discovery, and import functionality.
package skill

import (
	"sort"
	"strings"
)

// Suggestion represents a skill recommendation with confidence score.
type Suggestion struct {
	Name       string
	Confidence float64
	Reason     string
}

// Discovery finds relevant skills based on task descriptions.
type Discovery struct {
	skills []Skill
}

// NewDiscovery creates a new skill discovery instance.
//
// Expected:
//   - skills is a slice of available Skill values to match against.
//
// Returns:
//   - A configured Discovery instance.
//
// Side effects:
//   - None.
func NewDiscovery(skills []Skill) *Discovery {
	return &Discovery{skills: skills}
}

// Suggest returns skills relevant to the given task description.
//
// Expected:
//   - taskDescription is the text describing the task to find skills for.
//
// Returns:
//   - A slice of Suggestion sorted by descending confidence, or nil if none match.
//
// Side effects:
//   - None.
func (sd *Discovery) Suggest(taskDescription string) []Suggestion {
	if taskDescription == "" || len(sd.skills) == 0 {
		return nil
	}

	taskTokens := tokenize(taskDescription)
	if len(taskTokens) == 0 {
		return nil
	}

	var suggestions []Suggestion

	for i := range sd.skills {
		score, reason := sd.scoreSkill(sd.skills[i], taskTokens)
		if score >= 0.3 {
			suggestions = append(suggestions, Suggestion{
				Name:       sd.skills[i].Name,
				Confidence: score,
				Reason:     reason,
			})
		}
	}

	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Confidence > suggestions[j].Confidence
	})

	return suggestions
}

// scoreSkill calculates a relevance score for a skill against task tokens.
//
// Expected:
//   - s is a Skill to evaluate.
//   - taskTokens is a slice of tokens extracted from the task description.
//
// Returns:
//   - A normalised confidence score between 0 and 1.
//   - A reason string describing which fields matched.
//
// Side effects:
//   - None.
func (sd *Discovery) scoreSkill(s Skill, taskTokens []string) (float64, string) {
	const (
		weightWhenToUse = 3.0
		weightCategory  = 2.0
		weightName      = 1.0
	)

	whenToUseTokens := tokenize(s.WhenToUse)
	categoryTokens := tokenize(s.Category)
	nameTokens := tokenize(s.Name)

	whenToUseMatches := countOverlap(taskTokens, whenToUseTokens)
	categoryMatches := countOverlap(taskTokens, categoryTokens)
	nameMatches := countOverlap(taskTokens, nameTokens)

	var totalWeightedMatches float64
	var matchedFields []string

	if whenToUseMatches > 0 {
		totalWeightedMatches += float64(whenToUseMatches) * weightWhenToUse
		matchedFields = append(matchedFields, "WhenToUse")
	}

	if categoryMatches > 0 {
		totalWeightedMatches += float64(categoryMatches) * weightCategory
		matchedFields = append(matchedFields, "Category")
	}

	if nameMatches > 0 {
		totalWeightedMatches += float64(nameMatches) * weightName
		matchedFields = append(matchedFields, "Name")
	}

	allFieldTokens := len(whenToUseTokens) + len(categoryTokens) + len(nameTokens)
	if allFieldTokens == 0 {
		return 0, ""
	}

	maxPossible := float64(len(taskTokens)) * weightWhenToUse
	normalizedScore := totalWeightedMatches / maxPossible

	reason := ""
	if len(matchedFields) > 0 {
		reason = "matched in " + strings.Join(matchedFields, ", ")
	}

	return normalizedScore, reason
}

// tokenize converts text into a slice of cleaned tokens for matching.
//
// Expected:
//   - text is a string to tokenise.
//
// Returns:
//   - A slice of tokens with punctuation removed and length > 1.
//
// Side effects:
//   - None.
func tokenize(text string) []string {
	text = strings.ToLower(text)
	text = strings.ReplaceAll(text, "-", " ")
	text = strings.ReplaceAll(text, "_", " ")

	words := strings.Fields(text)

	var tokens []string
	for _, w := range words {
		cleaned := strings.Trim(w, ".,;:!?()[]{}\"'")
		if len(cleaned) > 1 {
			tokens = append(tokens, cleaned)
		}
	}
	return tokens
}

// countOverlap counts how many task tokens match field tokens.
//
// Expected:
//   - taskTokens is a slice of tokens from the task description.
//   - fieldTokens is a slice of tokens from a skill field.
//
// Returns:
//   - The count of matching tokens.
//
// Side effects:
//   - None.
func countOverlap(taskTokens, fieldTokens []string) int {
	count := 0
	for _, taskToken := range taskTokens {
		for _, fieldToken := range fieldTokens {
			if matchTokens(taskToken, fieldToken) {
				count++
				break
			}
		}
	}
	return count
}

// matchTokens checks if two tokens match exactly or by prefix.
//
// Expected:
//   - a and b are tokens to compare.
//
// Returns:
//   - True if tokens match exactly or share a 3-character prefix.
//
// Side effects:
//   - None.
func matchTokens(a, b string) bool {
	if a == b {
		return true
	}
	if len(a) >= 3 && len(b) >= 3 {
		if strings.HasPrefix(a, b[:3]) || strings.HasPrefix(b, a[:3]) {
			return true
		}
	}
	return false
}
