package intents

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/tui/navigation"
	"github.com/baphled/flowstate/internal/tui/themes"
	"github.com/baphled/flowstate/internal/tui/uikit/feedback"
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/tui/uikit/primitives"
)

// CreateStandardView produces the base ScreenLayout that all intent View methods should
// start from. The layout includes terminal-aware sizing, an optional logo header, and
// automatic state-driven modal overlays (error, loading, progress, success) so that
// intents do not need to manage feedback display themselves.
//
// Expected:
//   - b must be a non-nil BaseIntent with terminal info already configured via Init.
//
// Returns:
//   - A fully configured ScreenLayout ready for content and help text.
//
// Side effects:
//   - None.
//
// Example usage:
//
//	func (i *MyIntent) View() string {
//	    view := CreateStandardView(i.BaseIntent)
//	    view.WithContent(i.renderContent())
//	    view.WithHelp("↑/k Up  ↓/j Down  Enter Select  Esc Back")
//	    return view.Render()
//	}
func CreateStandardView(b *BaseIntent) *layout.ScreenLayout {
	view := layout.NewScreenLayout(b.GetTerminalInfo())

	if logo := b.GetLogo(); logo != nil {
		view.WithLogo(logo, b.GetLogoSpacing())
	}

	applyStateModals(view, b)

	view.SetUseFullWidth(true)
	return view
}

// CreateStandardViewWithBreadcrumbs extends CreateStandardView by adding a breadcrumb
// trail in the header area. Use this instead of CreateStandardView when the intent has
// multi-level navigation and the user needs to see their current location in the hierarchy.
//
// Expected:
//   - b must be a non-nil BaseIntent with terminal info already configured via Init.
//   - crumbs must be ordered from root to current location (e.g., "Main Menu", "Settings", "Display").
//
// Returns:
//   - A fully configured ScreenLayout with breadcrumbs applied.
//
// Side effects:
//   - None.
//
// Example usage:
//
//	view := CreateStandardViewWithBreadcrumbs(i.BaseIntent, "Main Menu", "Settings", "Display")
func CreateStandardViewWithBreadcrumbs(b *BaseIntent, crumbs ...string) *layout.ScreenLayout {
	view := CreateStandardView(b)
	view.WithBreadcrumbs(crumbs...)
	return view
}

// applyStateModals overlays the highest-priority feedback modal onto a ScreenLayout
// based on the current BaseIntent state. At most one modal is shown, following this
// priority order:
// 1. Error (most critical, with bell).
// 2. Loading (ongoing operation).
// 3. Progress (specific progress tracking).
// 4. Success (least critical, auto-dismiss).
func applyStateModals(view *layout.ScreenLayout, base *BaseIntent) {
	if base.HasError() {
		title := extractErrorTitle(base.GetError())
		modal := feedback.NewErrorModal(title, base.GetError().Error())
		view.ShowModalOverlay(modal)
	} else if base.IsLoading() {
		modal := feedback.NewLoadingModal(base.GetLoadingMessage(), true)
		view.ShowModalOverlay(modal)
	} else if base.IsProgressEnabled() {
		title, message, value := base.GetProgress()
		modal := feedback.NewProgressModal(title, message, value)
		view.ShowModalOverlay(modal)
	} else if base.ShouldShowSuccess() {
		modal := feedback.NewSuccessModal(base.GetSuccessMessage())
		view.ShowModalOverlay(modal)
	}
}

// extractErrorTitle derives a user-facing modal title from an error by scanning the
// message text for well-known keywords (e.g., "validation", "timeout", "not found").
// It unwraps nested errors to improve classification accuracy and falls back to
// "Error" when no keyword matches.
func extractErrorTitle(err error) string {
	if err == nil {
		return "Error"
	}

	errMsg := err.Error()

	prefixes := map[string]string{
		"validation":   "Validation Error",
		"database":     "Database Error",
		"network":      "Network Error",
		"permission":   "Permission Denied",
		"not found":    "Not Found",
		"timeout":      "Timeout",
		"unauthorized": "Unauthorized",
		"invalid":      "Invalid Input",
		"failed to":    "Operation Failed",
		"unable to":    "Operation Failed",
		"cannot":       "Operation Failed",
	}

	lowerMsg := strings.ToLower(errMsg)
	for prefix, title := range prefixes {
		if strings.Contains(lowerMsg, prefix) {
			return title
		}
	}

	var unwrapped = err
	for unwrapped != nil {
		if msg := unwrapped.Error(); msg != errMsg {
			lowerUnwrapped := strings.ToLower(msg)
			for prefix, title := range prefixes {
				if strings.Contains(lowerUnwrapped, prefix) {
					return title
				}
			}
		}
		unwrapped = errors.Unwrap(unwrapped)
	}

	return "Error"
}

// ThemedNavigationFooter renders the standard Navigate / Select / Back help badges
// for list views, menu selections, and general browsing screens.
//
// Expected:
//   - theme must be a non-nil Theme; badge colors and spacing are derived from it.
//
// Returns:
//   - A rendered string of navigation help badges.
//
// Side effects:
//   - None.
func ThemedNavigationFooter(theme themes.Theme) string {
	return primitives.RenderHelpFooter(theme,
		primitives.NavigateBadge(theme),
		primitives.SelectBadge(theme),
		primitives.BackBadge(theme),
	)
}

// ThemedFormFooter renders the Next / Prev / Submit / Cancel help badges
// appropriate for screens containing form inputs and field navigation.
//
// Expected:
//   - theme must be a non-nil Theme; badge colors and spacing are derived from it.
//
// Returns:
//   - A rendered string of form help badges.
//
// Side effects:
//   - None.
func ThemedFormFooter(theme themes.Theme) string {
	return primitives.RenderHelpFooter(theme,
		primitives.NextBadge(theme),
		primitives.PrevBadge(theme),
		primitives.SubmitBadge(theme),
		primitives.CancelBadge(theme),
	)
}

// ThemedListFooter renders Navigate / Select / Search / Back help badges for
// list views that support both keyboard navigation and incremental search.
//
// Expected:
//   - theme must be a non-nil Theme; badge colors and spacing are derived from it.
//
// Returns:
//   - A rendered string of list view help badges.
//
// Side effects:
//   - None.
func ThemedListFooter(theme themes.Theme) string {
	return primitives.RenderHelpFooter(theme,
		primitives.NavigateBadge(theme),
		primitives.SelectBadge(theme),
		primitives.SearchBadge(theme),
		primitives.BackBadge(theme),
	)
}

// ThemedDetailViewFooter renders Scroll / Back help badges for read-only detail
// screens where the primary interaction is vertical scrolling through content.
//
// Expected:
//   - theme must be a non-nil Theme; badge colors and spacing are derived from it.
//
// Returns:
//   - A rendered string of detail view help badges.
//
// Side effects:
//   - None.
func ThemedDetailViewFooter(theme themes.Theme) string {
	return primitives.RenderHelpFooter(theme,
		primitives.HelpKeyBadge("↑/↓", "Scroll", theme),
		primitives.BackBadge(theme),
	)
}

// ThemedCustomFooter composes an ad-hoc help footer from caller-supplied badges.
// Use this when none of the standard footer functions (ThemedNavigationFooter,
// ThemedFormFooter, etc.) match the screen's shortcut set.
//
// Expected:
//   - theme must be a non-nil Theme; used for overall footer layout styling.
//   - badges must each be a non-nil Badge created with the same theme for visual consistency.
//
// Returns:
//   - A rendered string of the custom help badges.
//
// Side effects:
//   - None.
//
// Example:
//
//	footer := ThemedCustomFooter(theme,
//	    primitives.NavigateBadge(theme),
//	    primitives.HelpKeyBadge("f", "Filter", theme),
//	    primitives.HelpKeyBadge("Enter", "View Details", theme),
//	    primitives.QuitBadge(theme),
//	)
func ThemedCustomFooter(theme themes.Theme, badges ...*primitives.Badge) string {
	return primitives.RenderHelpFooter(theme, badges...)
}

// ThemedGlobalBadges renders the application-wide Quit badge that should appear on
// every screen. Append the result to a screen-specific footer via CombineThemedFooters
// to ensure consistent global shortcut visibility.
//
// Expected:
//   - theme must be a non-nil Theme; badge colors and spacing are derived from it.
//
// Returns:
//   - A rendered string of the global help badges.
//
// Side effects:
//   - None.
func ThemedGlobalBadges(theme themes.Theme) string {
	return primitives.RenderHelpFooter(theme,
		primitives.QuitBadge(theme),
	)
}

// CombineThemedFooters joins multiple pre-rendered footer segments into a single
// help line. Empty or whitespace-only segments are silently dropped. Unlike
// CombineFooters, no extra separators are inserted because KeyBadges already
// include their own visual spacing.
//
// Expected:
//   - footers should be strings previously rendered by Themed*Footer or ThemedCustomFooter.
//
// Returns:
//   - A single string joining all non-empty footers with double-space separators.
//
// Side effects:
//   - None.
func CombineThemedFooters(footers ...string) string {
	var nonEmpty []string
	for _, footer := range footers {
		if strings.TrimSpace(footer) != "" {
			nonEmpty = append(nonEmpty, footer)
		}
	}
	if len(nonEmpty) == 0 {
		return ""
	}
	return strings.Join(nonEmpty, "  ")
}

// GlobalKeyResult represents the result of handling a global key.
type GlobalKeyResult int

const (
	// KeyNotHandled indicates the key was not a global key.
	KeyNotHandled GlobalKeyResult = iota
	// KeyQuit indicates the user wants to quit the application.
	KeyQuit
	// KeyHelp indicates the user wants to see help.
	KeyHelp
	// KeyBack indicates the user wants to go back.
	KeyBack
)

// HandleGlobalKeys classifies a key message as a global shortcut (help, back) or
// returns KeyNotHandled so the caller can proceed with intent-specific bindings.
// Call this at the top of every intent Update method to guarantee that global keys
// are never swallowed by sub-component handlers.
//
// Per docs/KEYBOARD_REFERENCE.md:
//   - q/ctrl+c: Quit application.
//   - ?: Show context-sensitive help.
//   - esc: Go back / Cancel.
//
// Expected:
//   - msg must be a non-zero tea.KeyMsg obtained from a tea.Msg type assertion.
//
// Returns:
//   - A GlobalKeyResult indicating which global key was matched, or KeyNotHandled.
//
// Side effects:
//   - None.
//
// Example usage:
//
//	func (i *MyIntent) Update(msg tea.Msg) tea.Cmd {
//	    if keyMsg, ok := msg.(tea.KeyMsg); ok {
//	        switch HandleGlobalKeys(keyMsg) {
//	        case KeyQuit:
//	            return tea.Quit
//	        case KeyHelp:
//	            i.helpModal.Toggle()
//	            return nil
//	        case KeyBack:
//	            return i.handleBack()
//	        }
//	    }
//	    // ... handle intent-specific keys
//	}
func HandleGlobalKeys(msg tea.KeyMsg) GlobalKeyResult {
	globalKeys := navigation.DefaultGlobalKeyMap()

	switch {
	case key.Matches(msg, globalKeys.Help):
		return KeyHelp
	case key.Matches(msg, globalKeys.Back):
		return KeyBack
	}

	return KeyNotHandled
}

// MessageInterceptor provides a middleware layer for handling global keys before delegation.
// This ensures escape, quit, and other global keys are always processed first,
// preventing sub-components (forms, modals) from consuming them.
//
// Usage:
//
//	func (i *Intent) updateWithForm(msg tea.Msg) tea.Cmd {
//	    interceptor := NewMessageInterceptor()
//	    return interceptor.
//	        OnBack(func() tea.Cmd {
//	            i.state = previousState
//	            return nil
//	        }).
//	        OnQuit(func() tea.Cmd {
//	            return tea.Quit
//	        }).
//	        OnHelp(func() tea.Cmd {
//	            i.ToggleHelp()
//	            return nil
//	        }).
//	        InterceptOr(msg, func() tea.Cmd {
//	            // Only called if no global keys matched
//	            return i.formModel.Update(msg)
//	        })
//	}
type MessageInterceptor struct {
	backHandler GlobalKeyHandler
	quitHandler GlobalKeyHandler
	helpHandler GlobalKeyHandler
}

// GlobalKeyHandler is a function that handles a global key event.
type GlobalKeyHandler func() tea.Cmd

// NewMessageInterceptor initializes an unconfigured MessageInterceptor.
//
// Returns:
//   - A fully initialized MessageInterceptor ready for use.
//
// Side effects:
//   - None.
func NewMessageInterceptor() *MessageInterceptor {
	return &MessageInterceptor{}
}

// OnBack configures the behavior triggered when the user presses Escape.
// Typically used to revert to a previous intent state or cancel an in-progress operation.
//
// Expected:
//   - handler must be non-nil; a nil handler is equivalent to not registering one.
//
// Returns:
//   - The same MessageInterceptor for method chaining.
//
// Side effects:
//   - Replaces any previously registered back handler.
func (m *MessageInterceptor) OnBack(handler GlobalKeyHandler) *MessageInterceptor {
	m.backHandler = handler
	return m
}

// OnQuit configures the behavior triggered when the user presses q or Ctrl+C.
// The handler should typically return tea.Quit, but may perform cleanup first.
//
// Expected:
//   - handler must be non-nil; a nil handler is equivalent to not registering one.
//
// Returns:
//   - The same MessageInterceptor for method chaining.
//
// Side effects:
//   - Replaces any previously registered quit handler.
func (m *MessageInterceptor) OnQuit(handler GlobalKeyHandler) *MessageInterceptor {
	m.quitHandler = handler
	return m
}

// OnHelp configures the behavior triggered when the user presses the ? key.
// The handler typically toggles a help modal via BaseIntent.ToggleHelp.
//
// Expected:
//   - handler must be non-nil; a nil handler is equivalent to not registering one.
//
// Returns:
//   - The same MessageInterceptor for method chaining.
//
// Side effects:
//   - Replaces any previously registered help handler.
func (m *MessageInterceptor) OnHelp(handler GlobalKeyHandler) *MessageInterceptor {
	m.helpHandler = handler
	return m
}

// InterceptOr runs the global key check before delegating to a sub-component.
// If msg is a key event matching a registered global handler, that handler runs and
// the fallback is skipped. Otherwise the fallback executes, letting the sub-component
// (form, modal, table) process the message normally. This prevents sub-components
// from accidentally consuming Escape, ?, or quit keys.
//
// Expected:
//   - msg may be any tea.Msg; non-key messages always pass through to fallback.
//   - fallback must be non-nil and should delegate msg to the active sub-component.
//
// Returns:
//   - tea.Cmd from the matched global key handler, OR
//   - tea.Cmd from the fallback function if no global keys matched.
//
// Side effects:
//   - Invokes exactly one handler: either the matching global key handler or the fallback.
func (m *MessageInterceptor) InterceptOr(msg tea.Msg, fallback func() tea.Cmd) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return fallback()
	}

	result := HandleGlobalKeys(keyMsg)

	switch result {
	case KeyBack:
		if m.backHandler != nil {
			return m.backHandler()
		}
	case KeyQuit:
		if m.quitHandler != nil {
			return m.quitHandler()
		}
	case KeyHelp:
		if m.helpHandler != nil {
			return m.helpHandler()
		}
	}

	return fallback()
}

// StandardQuitHandler provides the default quit behavior for use with MessageInterceptor.
//
// Returns:
//   - A GlobalKeyHandler that returns tea.Quit.
//
// Side effects:
//   - None.
func StandardQuitHandler() GlobalKeyHandler {
	return func() tea.Cmd {
		return tea.Quit
	}
}

// StandardHelpHandler provides the default help-toggle behavior for use with
// MessageInterceptor.OnHelp. The returned handler calls ToggleHelp on the
// given BaseIntent, which is appropriate for most intents that use the
// built-in help modal.
//
// Expected:
//   - intent must be a non-nil BaseIntent that has been initialized with help modal support.
//
// Returns:
//   - A GlobalKeyHandler that toggles the help modal and returns nil.
//
// Side effects:
//   - None directly; the returned handler toggles help visibility when invoked.
//
// Example usage:
//
//	interceptor.OnHelp(StandardHelpHandler(i.BaseIntent))
func StandardHelpHandler(intent *BaseIntent) GlobalKeyHandler {
	return func() tea.Cmd {
		intent.ToggleHelp()
		return nil
	}
}
