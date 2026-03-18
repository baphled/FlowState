// Package discovery provides agent discovery using weighted token matching.
package discovery

import (
	"sort"
	"strings"

	"github.com/baphled/flowstate/internal/agent"
)

// AgentSuggestion represents a suggested agent with its confidence score.
type AgentSuggestion struct {
	AgentID    string
	Confidence float64
	Reason     string
}

// AgentDiscovery matches user messages to appropriate agents.
type AgentDiscovery struct {
	manifests []agent.Manifest
}

// NewAgentDiscovery creates a new AgentDiscovery with the given manifests.
//
// Expected:
//   - manifests is a slice of agent manifests to match against.
//
// Returns:
//   - A configured AgentDiscovery instance.
//
// Side effects:
//   - None.
func NewAgentDiscovery(manifests []agent.Manifest) *AgentDiscovery {
	return &AgentDiscovery{manifests: manifests}
}

// Suggest returns agent suggestions for the given message, sorted by confidence.
//
// Expected:
//   - message is the user input to match against agent manifests.
//
// Returns:
//   - A slice of AgentSuggestion sorted by descending confidence, or nil if none match.
//
// Side effects:
//   - None.
func (ad *AgentDiscovery) Suggest(message string) []AgentSuggestion {
	if message == "" || len(ad.manifests) == 0 {
		return nil
	}

	msgTokens := tokenize(message)
	if len(msgTokens) == 0 {
		return nil
	}

	var suggestions []AgentSuggestion

	for i := range ad.manifests {
		score, reason := ad.scoreManifest(&ad.manifests[i], msgTokens)
		if score >= 0.3 {
			suggestions = append(suggestions, AgentSuggestion{
				AgentID:    ad.manifests[i].ID,
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

func (ad *AgentDiscovery) scoreManifest(m *agent.Manifest, msgTokens []string) (float64, string) {
	const (
		weightWhenToUse = 3.0
		weightRole      = 2.0
		weightGoal      = 1.0
	)

	whenToUseTokens := tokenize(m.Metadata.WhenToUse)
	roleTokens := tokenize(m.Metadata.Role)
	goalTokens := tokenize(m.Metadata.Goal)

	whenToUseMatches := countOverlap(msgTokens, whenToUseTokens)
	roleMatches := countOverlap(msgTokens, roleTokens)
	goalMatches := countOverlap(msgTokens, goalTokens)

	var totalWeightedMatches float64
	var matchedFields []string

	if whenToUseMatches > 0 {
		totalWeightedMatches += float64(whenToUseMatches) * weightWhenToUse
		matchedFields = append(matchedFields, "WhenToUse")
	}

	if roleMatches > 0 {
		totalWeightedMatches += float64(roleMatches) * weightRole
		matchedFields = append(matchedFields, "Role")
	}

	if goalMatches > 0 {
		totalWeightedMatches += float64(goalMatches) * weightGoal
		matchedFields = append(matchedFields, "Goal")
	}

	allFieldTokens := len(whenToUseTokens) + len(roleTokens) + len(goalTokens)
	if allFieldTokens == 0 {
		return 0, ""
	}

	maxPossible := float64(len(msgTokens)) * weightWhenToUse
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

func countOverlap(msgTokens, fieldTokens []string) int {
	count := 0
	for _, msgToken := range msgTokens {
		for _, fieldToken := range fieldTokens {
			if matchTokens(msgToken, fieldToken) {
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
