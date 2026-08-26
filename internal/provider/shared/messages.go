package shared

import "github.com/baphled/flowstate/internal/provider"

// RolePair holds the role and content extracted from a provider.Message.
// It is an intermediate representation used to map messages to provider-specific
// wire formats without duplicating the role/content extraction logic.
type RolePair struct {
	Role    string
	Content string
}

// ConvertMessagesToRolePairs extracts the role and content from each message in
// msgs and returns them as a slice of RolePair values, preserving the original
// order. Tool-call metadata and tool-call IDs are intentionally omitted; each
// provider is responsible for mapping those fields to its own wire format.
//
// Expected:
//   - msgs is a slice of provider Message values in conversation order.
//
// Returns:
//   - A slice of RolePair values with Role and Content populated, in the same
//     order as msgs. Returns an empty slice when msgs is empty.
//
// Side effects:
//   - None.
func ConvertMessagesToRolePairs(msgs []provider.Message) []RolePair {
	pairs := make([]RolePair, len(msgs))
	for i, m := range msgs {
		pairs[i] = RolePair{
			Role:    m.Role,
			Content: m.Content,
		}
	}
	return pairs
}
