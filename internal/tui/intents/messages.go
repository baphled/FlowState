package intents

// ShowModalMsg displays a modal overlay with the given Intent.
type ShowModalMsg struct {
	Modal Intent
}

// DismissModalMsg removes the active modal overlay.
type DismissModalMsg struct{}

// SwitchToIntentMsg switches to a new Intent.
type SwitchToIntentMsg struct {
	Intent Intent
}
