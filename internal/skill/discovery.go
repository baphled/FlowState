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
func NewDiscovery(skills []Skill) *Discovery {
	return &Discovery{skills: skills}
}

// Suggest returns skills relevant to the given task description.
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
