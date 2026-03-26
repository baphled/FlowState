package chat

import (
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	chatview "github.com/baphled/flowstate/internal/tui/views/chat"
	tea "github.com/charmbracelet/bubbletea"
)

// DelegationStatusComponent wraps the DelegationStatusWidget to manage state
// and render the current delegation progress.
type DelegationStatusComponent struct {
	info   *provider.DelegationInfo
	widget *chatview.DelegationStatusWidget
	theme  theme.Theme
}

// NewDelegationStatusComponent creates a new delegation status component.
//
// Expected:
//   - t is a valid theme value.
//
// Returns:
//   - A component ready to receive delegation data.
//
// Side effects:
//   - None.
func NewDelegationStatusComponent(t theme.Theme) *DelegationStatusComponent {
	return &DelegationStatusComponent{
		theme: t,
	}
}

// Update processes Bubble Tea messages.
//
// Expected:
//   - msg is a Bubble Tea message emitted by the intent loop.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - May update the underlying widget when animation messages arrive.
func (c *DelegationStatusComponent) Update(msg tea.Msg) {
	if c.widget != nil {
		if tick, ok := msg.(SpinnerTickMsg); ok {
			// In a real implementation, we'd cast to int if SpinnerTickMsg carried value,
			// or just increment. Here we assume the intent manages the tick frame.
			// But the widget expects SetFrame to be called externally or handled here.
			// Since SpinnerTickMsg is empty struct in intent.go, we can't get frame from it easily
			// without state. But wait, the Intent passes tickFrame to View.
			// Let's assume the Intent calls SetFrame on this component or we handle it here.
			// For simplicity, let's just say Update handles data updates.
			_ = tick
		}
	}
}

// SetInfo updates the delegation info and recreates the widget if needed.
//
// Expected:
//   - info may be nil when the component should be cleared.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Replaces the cached widget and may discard existing render state.
func (c *DelegationStatusComponent) SetInfo(info *provider.DelegationInfo) {
	c.info = info
	if info != nil {
		c.widget = chatview.NewDelegationStatusWidget(info, c.theme)
	} else {
		c.widget = nil
	}
}

// SetFrame updates the animation frame on the underlying widget.
//
// Expected:
//   - frame is the current spinner frame.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates the embedded widget animation state when present.
func (c *DelegationStatusComponent) SetFrame(frame int) {
	if c.widget != nil {
		c.widget.SetFrame(frame)
	}
}

// View renders the component.
//
// Expected:
//   - The component may or may not have a widget configured.
//
// Returns:
//   - The rendered widget content, or an empty string when unset.
//
// Side effects:
//   - None.
func (c *DelegationStatusComponent) View() string {
	if c.widget == nil {
		return ""
	}
	return c.widget.Render()
}
