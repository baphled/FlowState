package sessionbrowser

// ActionKey identifies a session browser action.
type ActionKey string

const (
	// ActionSelect indicates the user selected an existing session.
	ActionSelect ActionKey = "session.select"
	// ActionCreate indicates the user wants to create a new session.
	ActionCreate ActionKey = "session.create"
	// ActionCancel indicates the user cancelled the session browser.
	ActionCancel ActionKey = "session.cancel"
)

// Nav carries the action and its typed payload.
type Nav struct {
	Action    ActionKey
	SessionID string
}
