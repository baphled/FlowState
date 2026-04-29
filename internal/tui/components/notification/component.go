package notification

import (
	"strconv"
	"time"

	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TickMsg is sent periodically to drive notification expiry.
type TickMsg struct{}

// Component is a Bubble Tea component that renders active notifications as an overlay.
type Component struct {
	manager Manager
	width   int
}

// NewComponent creates a Component backed by the given manager.
//
// Expected:
//   - manager is non-nil.
//
// Returns:
//   - An initialised Component.
//
// Side effects:
//   - None.
func NewComponent(manager Manager) *Component {
	return &Component{manager: manager}
}

// SetWidth sets the available width for rendering.
//
// Expected:
//   - width > 0.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates width.
func (c *Component) SetWidth(width int) {
	c.width = width
}

// Manager returns the underlying notification Manager.
//
// Returns:
//   - The Manager instance backing this Component.
//
// Side effects:
//   - None.
func (c *Component) Manager() Manager {
	return c.manager
}

// Init returns a tick command to drive notification expiry.
//
// Expected:
//   - None.
//
// Returns:
//   - A Cmd that fires TickMsg after 500ms.
//
// Side effects:
//   - None.
func (c *Component) Init() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return TickMsg{} })
}

// Update handles TickMsg to purge expired notifications.
//
// Expected:
//   - msg is any Bubble Tea message.
//
// Returns:
//   - A Cmd to schedule the next tick when msg is TickMsg, nil otherwise.
//
// Side effects:
//   - May remove expired notifications from the manager.
func (c *Component) Update(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(TickMsg); ok {
		c.purgeExpired()
		return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return TickMsg{} })
	}
	return nil
}

// purgeExpired removes expired notifications from the manager.
//
// Expected:
//   - manager is non-nil.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Calls Dismiss on each expired notification.
func (c *Component) purgeExpired() {
	for _, n := range c.manager.Expired() {
		c.manager.Dismiss(n.ID)
	}
}

// View renders active notifications stacked vertically, or empty string when none.
//
// Expected:
//   - None.
//
// Returns:
//   - A lipgloss-rendered string or empty string.
//
// Side effects:
//   - None.
func (c *Component) View() string {
	active := c.manager.Active()
	if len(active) == 0 {
		return ""
	}
	var lines []string
	for i := len(active) - 1; i >= 0; i-- {
		lines = append(lines, c.renderOne(active[i]))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderOne renders a single notification with icon, title, and message.
//
// Expected:
//   - n is a valid Notification.
//
// Returns:
//   - A styled single-line string.
//
// Side effects:
//   - None.
func (c *Component) renderOne(n Notification) string {
	icon := levelIcon(n.Level)
	style := levelStyle(n.Level)
	return style.Render(icon + " " + n.Title + ": " + n.Message)
}

// levelIcon returns the display icon for a notification level.
//
// Expected:
//   - level is one of the defined Level constants.
//
// Returns:
//   - A single Unicode icon character.
//
// Side effects:
//   - None.
func levelIcon(level Level) string {
	switch level {
	case LevelSuccess:
		return "✓"
	case LevelWarning:
		return "⚠"
	case LevelError:
		return "✗"
	default:
		return "ℹ"
	}
}

// levelStyle returns a lipgloss style for the given notification level.
//
// Expected:
//   - level is one of the defined Level constants.
//
// Returns:
//   - A lipgloss.Style appropriate for the level.
//
// Side effects:
//   - None.
func levelStyle(level Level) lipgloss.Style {
	switch level {
	case LevelSuccess:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	case LevelWarning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	case LevelError:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	}
}

// AddDelegationNotification adds a notification for the given delegation event.
//
// Expected:
//   - info is non-nil; info.Status is "started", "completed", or "failed".
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Adds a Notification to the manager.
func (c *Component) AddDelegationNotification(info *provider.DelegationInfo) {
	switch info.Status {
	case "started":
		c.manager.Add(Notification{
			ID:        info.ChainID,
			Title:     "Delegation",
			Message:   info.TargetAgent + " started",
			Level:     LevelInfo,
			Duration:  3 * time.Second,
			CreatedAt: time.Now(),
		})
	case "completed":
		c.manager.Add(Notification{
			ID:        info.ChainID,
			Title:     "Delegation",
			Message:   info.TargetAgent + " completed",
			Level:     LevelSuccess,
			Duration:  5 * time.Second,
			CreatedAt: time.Now(),
		})
	case "failed":
		c.manager.Add(Notification{
			ID:        info.ChainID,
			Title:     "Delegation",
			Message:   info.TargetAgent + " failed",
			Level:     LevelError,
			Duration:  8 * time.Second,
			CreatedAt: time.Now(),
		})
	}
}

// AddProviderErrorNotification adds a notification for a provider error event.
//
// Expected:
//   - event is a non-nil ProviderErrorEvent.
//   - event.Data.SessionID matches the current session.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Adds a Notification to the manager.
func (c *Component) AddProviderErrorNotification(event *events.ProviderErrorEvent) {
	errorMsg := "Unknown error"
	if event.Data.Error != nil {
		errorMsg = event.Data.Error.Error()
	}
	c.manager.Add(Notification{
		ID:        "provider-error-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Title:     "Provider Error",
		Message:   event.Data.ProviderName + ": " + errorMsg,
		Level:     LevelError,
		Duration:  8 * time.Second,
		CreatedAt: time.Now(),
	})
}

// AddProviderRateLimitedNotification adds a notification for a provider rate limit event.
//
// Expected:
//   - event is a non-nil ProviderEvent.
//   - event.Data.SessionID matches the current session.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Adds a Notification to the manager.
func (c *Component) AddProviderRateLimitedNotification(event *events.ProviderEvent) {
	c.manager.Add(Notification{
		ID:        "provider-rate-limited-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Title:     "Rate Limited",
		Message:   event.Data.ProviderName + " is rate limited",
		Level:     LevelWarning,
		Duration:  5 * time.Second,
		CreatedAt: time.Now(),
	})
}

// AddCategoryModelSwapNotification adds a notification when the
// CategoryResolver auto-promoted a denied model to a capable
// alternative (see engine.CategoryModelSwap). Surfaces the upgrade in
// the activity overlay so the user sees, in the moment a delegation
// fires, that an agent's configured model was substituted because of
// the cluster-wide tool-capability allow/deny lists. Without this
// surface, swaps were only visible in `flowstate serve`'s stderr —
// invisible to TUI users.
//
// Takes primitives rather than an engine.CategoryModelSwap value to
// keep the notification package free of any internal/engine import,
// matching the existing helpers' decoupling discipline.
//
// Expected:
//   - category names the routing category (e.g. "quick", "deep").
//     Empty is allowed; renders as "(uncategorised)".
//   - original is the model the descriptor strategy first picked.
//   - chosen is the model the resolver actually returned. Must
//     differ from original (the resolver only fires the swap event
//     when it changed the result).
//   - reason is the human-readable cause from the resolver.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Adds a Notification to the manager at LevelWarning. Warning
//     level rather than Info because the user's configured model was
//     overridden — they may want to know to update their config.
func (c *Component) AddCategoryModelSwapNotification(category, original, chosen, reason string) {
	displayedCategory := category
	if displayedCategory == "" {
		displayedCategory = "(uncategorised)"
	}
	message := displayedCategory + ": " + original + " → " + chosen
	if reason != "" {
		message += " (" + reason + ")"
	}
	c.manager.Add(Notification{
		ID:        "category-model-swap-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Title:     "Model auto-promoted",
		Message:   message,
		Level:     LevelWarning,
		Duration:  10 * time.Second,
		CreatedAt: time.Now(),
	})
}

// AddToolExecuteErrorNotification adds a notification for a tool execution error event.
//
// Expected:
//   - event is a non-nil ToolExecuteErrorEvent.
//   - event.Data.SessionID matches the current session.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Adds a Notification to the manager.
func (c *Component) AddToolExecuteErrorNotification(event *events.ToolExecuteErrorEvent) {
	errorMsg := "Unknown error"
	if event.Data.Error != nil {
		errorMsg = event.Data.Error.Error()
	}
	c.manager.Add(Notification{
		ID:        "tool-error-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Title:     "Tool Error",
		Message:   event.Data.ToolName + ": " + errorMsg,
		Level:     LevelError,
		Duration:  8 * time.Second,
		CreatedAt: time.Now(),
	})
}
