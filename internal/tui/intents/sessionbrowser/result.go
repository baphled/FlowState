package sessionbrowser

import tuiintents "github.com/baphled/flowstate/internal/tui/intents"

// NewSelectResult creates an IntentResult for selecting an existing session.
//
// Expected:
//   - sessionID is a non-empty string identifying the session to select.
//
// Returns:
//   - An IntentResult with ActionSelect and the given sessionID.
//
// Side effects:
//   - None.
func NewSelectResult(sessionID string) *tuiintents.IntentResult {
	return &tuiintents.IntentResult{
		Data:   Nav{Action: ActionSelect, SessionID: sessionID},
		Action: string(ActionSelect),
	}
}

// NewCreateResult creates an IntentResult for creating a new session.
//
// Returns:
//   - An IntentResult with ActionCreate.
//
// Side effects:
//   - None.
func NewCreateResult() *tuiintents.IntentResult {
	return &tuiintents.IntentResult{
		Data:   Nav{Action: ActionCreate},
		Action: string(ActionCreate),
	}
}

// NewCancelResult creates an IntentResult for cancelling the browser.
//
// Returns:
//   - An IntentResult with ActionCancel.
//
// Side effects:
//   - None.
func NewCancelResult() *tuiintents.IntentResult {
	return &tuiintents.IntentResult{
		Data:   Nav{Action: ActionCancel},
		Action: string(ActionCancel),
	}
}
