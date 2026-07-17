package engine

import "strings"

func coordStoreKeyConvention(agentID string) string {
	switch agentID {
	case "explorer":
		return "codebase-findings"
	case "librarian":
		return "external-refs"
	case "analyst":
		return "analysis"
	case "plan-writer":
		return "plan"
	case "plan-reviewer":
		return "review"
	default:
		return ""
	}
}

func expectedCoordinationKey(agentID, chainID string) string {
	if chainID == "" {
		return ""
	}
	if suffix := coordStoreKeyConvention(agentID); suffix != "" {
		return chainID + "/" + suffix
	}
	return ""
}

func fallbackCoordinationFailureKey(agentID, chainID string) string {
	if agentID == "" || chainID == "" {
		return ""
	}
	return chainID + "/_engine_fallback/" + agentID + "/delivery_failure"
}

func expectedCoordinationStoreKeyFromMessage(message string) (string, bool) {
	const marker = "coordination_store key="
	idx := strings.Index(message, marker)
	if idx < 0 {
		return "", false
	}
	rest := message[idx+len(marker):]
	if strings.HasPrefix(rest, "**") {
		rest = rest[2:]
		end := strings.Index(rest, "**")
		if end < 0 {
			return "", false
		}
		key := strings.TrimSpace(rest[:end])
		return key, key != ""
	}
	end := len(rest)
	for i, r := range rest {
		switch r {
		case ' ', '\n', '\t':
			end = i
			goto done
		}
	}

done:
	key := strings.TrimRight(strings.TrimSpace(rest[:end]), ".,:;`*")
	return key, key != ""
}

func buildFreshDeliveryRetryMessage(key string) string {
	return "You are in final delivery mode. Call coordination_store with operation=set and write your final result to key " + key + ". " +
		"Do not delegate. Do not call todo tools. Do not call unrelated tools. Do not narrate your process. " +
		"If you need to include the result in multiple parts, write the first required part now to the exact required key."
}
