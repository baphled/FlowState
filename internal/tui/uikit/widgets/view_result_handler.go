package widgets

import tea "github.com/charmbracelet/bubbletea"

// ActionHandler processes action data and returns a command.
type ActionHandler func(data map[string]interface{}) tea.Cmd

// HandleViewResult processes a ViewResult using the provided action handlers.
// Actions maps action strings to their handlers.
// OnCancel is called when ResultCancel is received.
//
// Expected:
//   - result may be nil (returns nil immediately).
//   - actions maps action strings to their handler functions.
//   - onCancel is an optional callback invoked on ResultCancel.
//
// Returns:
//   - A tea.Cmd value.
//
// Side effects:
//   - Invokes onCancel callback when result type is ResultCancel.
func HandleViewResult(result ViewResult, actions map[string]ActionHandler, onCancel func()) tea.Cmd {
	if result == nil {
		return nil
	}
	switch result.Type() {
	case ResultNavigate:
		return handleNavigateData(result.Data(), actions)
	case ResultCancel:
		if onCancel != nil {
			onCancel()
		}
		return nil
	}
	return nil
}

// handleNavigateData dispatches navigation data to the matching action handler.
//
// Expected:
//   - data is a map[string]interface{} with an "action" key.
//
// Returns:
//   - A tea.Cmd from the matched handler, or nil if no match.
//
// Side effects:
//   - None.
func handleNavigateData(data interface{}, actions map[string]ActionHandler) tea.Cmd {
	actionData, ok := data.(map[string]interface{})
	if !ok {
		return nil
	}
	action, ok := actionData["action"].(string)
	if !ok {
		return nil
	}
	if handler, exists := actions[action]; exists {
		return handler(actionData)
	}
	return nil
}
