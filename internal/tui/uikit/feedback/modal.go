package feedback

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/tui/uikit/display"
	"github.com/baphled/flowstate/internal/ui/themes"
)

// ModalSpinnerTickMsg is sent periodically to advance the spinner animation.
type ModalSpinnerTickMsg struct{}

// ModalCountdownTickMsg is sent periodically to update the auto-dismiss countdown display.
type ModalCountdownTickMsg struct{}

// ModalAutoDismissMsg is sent when the auto-dismiss countdown reaches zero.
type ModalAutoDismissMsg struct{}

// ModalType defines the type of modal.
type ModalType int

const (
	// ModalError displays an error message.
	ModalError ModalType = iota
	// ModalLoading displays a loading message with spinner.
	ModalLoading
	// ModalProgress displays progress with a progress bar.
	ModalProgress
	// ModalSuccess displays a success message.
	ModalSuccess
	// ModalWarning displays a warning message.
	ModalWarning
)

// SimpleSpinner provides a simple text-based spinner animation.
type SimpleSpinner struct {
	frames []string
	index  int
}

// NewSimpleSpinner creates a new simple spinner.
//
// Returns:
//   - A fully initialized SimpleSpinner ready for use.
//
// Side effects:
//   - None.
func NewSimpleSpinner() *SimpleSpinner {
	return &SimpleSpinner{
		frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		index:  0,
	}
}

// GetFrame returns the current spinner frame.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (s *SimpleSpinner) GetFrame() string {
	return s.frames[s.index]
}

// Advance advances the spinner to the next frame.
//
// Side effects:
//   - None.
func (s *SimpleSpinner) Advance() {
	s.index = (s.index + 1) % len(s.frames)
}

// LoadingMessageRotator rotates through a list of loading messages.
type LoadingMessageRotator struct {
	messages []string
	index    int
}

// NewLoadingMessageRotator creates a new message rotator.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized LoadingMessageRotator ready for use.
//
// Side effects:
//   - None.
func NewLoadingMessageRotator(messages []string) *LoadingMessageRotator {
	if len(messages) == 0 {
		messages = []string{"Loading..."}
	}
	return &LoadingMessageRotator{
		messages: messages,
		index:    0,
	}
}

// GetCurrent returns the current message.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (r *LoadingMessageRotator) GetCurrent() string {
	return r.messages[r.index]
}

// Rotate advances to the next message and returns it.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (r *LoadingMessageRotator) Rotate() string {
	r.index = (r.index + 1) % len(r.messages)
	return r.messages[r.index]
}

// Modal represents the content and configuration of a modal
// This is the UIKit version that uses theme-based styling exclusively.
type Modal struct {
	Type               ModalType
	Title              string
	Message            string
	Progress           float64
	Actions            []string
	FadeInDuration     time.Duration
	AutoDismiss        time.Duration
	Bell               bool
	Cancellable        bool
	fadeStartTime      time.Time
	countdownRemaining int
	spinner            *SimpleSpinner
	messageRotator     *LoadingMessageRotator
	theme              themes.Theme
}

// getTheme returns the theme or default if nil.
//
// Returns:
//   - The assigned theme, or a default theme if none is set.
//
// Side effects:
//   - None.
func (m *Modal) getTheme() themes.Theme {
	if m.theme != nil {
		return m.theme
	}
	return themes.NewDefaultTheme()
}

// NewErrorModal creates a new error modal.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Modal ready for use.
//
// Side effects:
//   - None.
func NewErrorModal(title, message string) *Modal {
	return &Modal{
		Type:           ModalError,
		Title:          title,
		Message:        message,
		FadeInDuration: 150 * time.Millisecond,
		Bell:           true,
		Cancellable:    true,
		fadeStartTime:  time.Now(),
		theme:          themes.NewDefaultTheme(),
	}
}

// NewLoadingModal creates a new loading modal with optional spinner.
//
// Expected:
//   - Must be a valid string.
//   - bool must be valid.
//
// Returns:
//   - A fully initialized Modal ready for use.
//
// Side effects:
//   - None.
func NewLoadingModal(message string, cancellable bool) *Modal {
	return &Modal{
		Type:           ModalLoading,
		Title:          "Loading",
		Message:        message,
		FadeInDuration: 150 * time.Millisecond,
		Cancellable:    cancellable,
		fadeStartTime:  time.Now(),
		spinner:        NewSimpleSpinner(),
		theme:          themes.NewDefaultTheme(),
	}
}

// NewProgressModal creates a new progress modal.
//
// Expected:
//   - Must be a valid string.
//   - float64 must be valid.
//
// Returns:
//   - A fully initialized Modal ready for use.
//
// Side effects:
//   - None.
func NewProgressModal(title, message string, progress float64) *Modal {
	return &Modal{
		Type:           ModalProgress,
		Title:          title,
		Message:        message,
		Progress:       progress,
		FadeInDuration: 150 * time.Millisecond,
		fadeStartTime:  time.Now(),
		theme:          themes.NewDefaultTheme(),
	}
}

// NewSuccessModal creates a new success modal with auto-dismiss.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Modal ready for use.
//
// Side effects:
//   - None.
func NewSuccessModal(message string) *Modal {
	autoDismiss := 3 * time.Second
	return &Modal{
		Type:               ModalSuccess,
		Title:              "Success",
		Message:            message,
		FadeInDuration:     150 * time.Millisecond,
		AutoDismiss:        autoDismiss,
		fadeStartTime:      time.Now(),
		countdownRemaining: int(autoDismiss.Seconds()),
		theme:              themes.NewDefaultTheme(),
	}
}

// NewWarningModal creates a new warning modal.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Modal ready for use.
//
// Side effects:
//   - None.
func NewWarningModal(title, message string) *Modal {
	return &Modal{
		Type:           ModalWarning,
		Title:          title,
		Message:        message,
		FadeInDuration: 150 * time.Millisecond,
		Bell:           true,
		Cancellable:    true,
		fadeStartTime:  time.Now(),
		theme:          themes.NewDefaultTheme(),
	}
}

// SetMessageRotator sets a loading message rotator for dynamic messages.
//
// Expected:
//   - loadingmessagerotator must be valid.
//
// Returns:
//   - A fully initialized Modal ready for use.
//
// Side effects:
//   - None.
func (m *Modal) SetMessageRotator(rotator *LoadingMessageRotator) *Modal {
	m.messageRotator = rotator
	return m
}

// WithTheme sets the theme for the modal.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Modal ready for use.
//
// Side effects:
//   - None.
func (m *Modal) WithTheme(theme themes.Theme) *Modal {
	m.theme = theme
	return m
}

// Render renders the modal centered in the given terminal dimensions.
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (m *Modal) Render(terminalWidth, _ int) string {
	theme := m.getTheme()
	opacity := m.calculateOpacity()
	borderColor, icon := m.getStyleForType()
	message := m.buildMessage()
	maxModalWidth := m.modalMaxWidth(terminalWidth)
	maxTextWidth := modalTextWidth(maxModalWidth)
	content := m.buildModalContent(icon, message, maxTextWidth, theme)
	modalWidth := modalContentWidth(content, maxModalWidth)
	boxStyle := m.modalBoxStyle(borderColor, modalWidth, opacity)
	return boxStyle.Render(content)
}

// buildMessage returns the rendered message for the modal.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (m *Modal) buildMessage() string {
	message := m.Message
	if m.Type == ModalLoading && m.spinner != nil {
		message = fmt.Sprintf("%s %s", m.spinner.GetFrame(), m.Message)
	}
	if m.messageRotator == nil {
		return message
	}
	rotatedMessage := m.messageRotator.GetCurrent()
	if m.Type == ModalLoading && m.spinner != nil {
		return fmt.Sprintf("%s %s", m.spinner.GetFrame(), rotatedMessage)
	}
	return rotatedMessage
}

// modalMaxWidth calculates the max modal width for the terminal.
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - An int value.
//
// Side effects:
//   - None.
func (m *Modal) modalMaxWidth(terminalWidth int) int {
	maxModalWidth := 100
	if terminalWidth > 0 && terminalWidth-6 < maxModalWidth {
		maxModalWidth = terminalWidth - 6
	}
	if maxModalWidth < 40 {
		maxModalWidth = 40
	}
	return maxModalWidth
}

// modalTextWidth calculates the text wrap width for the modal.
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - An int value.
//
// Side effects:
//   - None.
func modalTextWidth(maxModalWidth int) int {
	maxTextWidth := maxModalWidth - 4
	if maxTextWidth < 20 {
		maxTextWidth = 20
	}
	return maxTextWidth
}

// buildModalContent assembles the modal content body.
//
// Expected:
//   - icon must be valid.
//   - message must be valid.
//   - int must be valid.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (m *Modal) buildModalContent(icon, message string, maxTextWidth int, theme themes.Theme) string {
	contentParts := m.baseContentParts(icon)
	contentParts = append(contentParts, wrapText(message, maxTextWidth))
	contentParts = m.appendProgressContent(contentParts, maxTextWidth, theme)
	contentParts = m.appendActionsContent(contentParts)
	contentParts = m.appendDismissalContent(contentParts)
	contentParts = m.appendAutoDismissContent(contentParts)
	return strings.Join(contentParts, "\n")
}

// baseContentParts returns the initial content parts for the modal.
//
// Expected:
//   - icon must be valid.
//
// Returns:
//   - A string slice value.
//
// Side effects:
//   - None.
func (m *Modal) baseContentParts(icon string) []string {
	if m.Title == "" {
		return []string{}
	}
	return []string{fmt.Sprintf("%s %s", icon, m.Title), ""}
}

// appendProgressContent appends progress bar content when needed.
//
// Expected:
//   - contentParts must be valid.
//   - int must be valid.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A string slice value.
//
// Side effects:
//   - None.
func (m *Modal) appendProgressContent(contentParts []string, maxTextWidth int, theme themes.Theme) []string {
	if m.Type != ModalProgress {
		return contentParts
	}
	return append(contentParts, "", m.renderProgressBar(maxTextWidth, theme))
}

// appendActionsContent appends actions content when present.
//
// Expected:
//   - contentParts must be valid.
//
// Returns:
//   - A string slice value.
//
// Side effects:
//   - None.
func (m *Modal) appendActionsContent(contentParts []string) []string {
	if len(m.Actions) == 0 {
		return contentParts
	}
	return append(contentParts, "", strings.Join(m.Actions, "  "))
}

// appendDismissalContent appends dismissal hints when cancellable.
//
// Expected:
//   - contentParts must be valid.
//
// Returns:
//   - A string slice value.
//
// Side effects:
//   - None.
func (m *Modal) appendDismissalContent(contentParts []string) []string {
	if !m.Cancellable {
		return contentParts
	}
	return append(contentParts, "", dismissalHint(m.Type))
}

// appendAutoDismissContent appends auto-dismiss content when active.
//
// Expected:
//   - contentParts must be valid.
//
// Returns:
//   - A string slice value.
//
// Side effects:
//   - None.
func (m *Modal) appendAutoDismissContent(contentParts []string) []string {
	if m.AutoDismiss <= 0 || m.countdownRemaining <= 0 {
		return contentParts
	}
	return append(contentParts, "", fmt.Sprintf("Auto-dismiss in %ds", m.countdownRemaining))
}

// dismissalHint returns the dismissal hint text for the modal type.
//
// Expected:
//   - modalType must be valid.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func dismissalHint(modalType ModalType) string {
	switch modalType {
	case ModalError:
		return "Press Esc to dismiss"
	case ModalLoading:
		return "Press Esc to cancel"
	default:
		return "Press Esc to close"
	}
}

// modalContentWidth calculates the final modal width based on content.
//
// Expected:
//   - content must be valid.
//   - int must be valid.
//
// Returns:
//   - An int value.
//
// Side effects:
//   - None.
func modalContentWidth(content string, maxModalWidth int) int {
	contentLines := strings.Split(content, "\n")
	contentWidth := 0
	for _, line := range contentLines {
		lineWidth := lipgloss.Width(line)
		if lineWidth > contentWidth {
			contentWidth = lineWidth
		}
	}
	modalWidth := contentWidth + 4
	if modalWidth < 40 {
		modalWidth = 40
	}
	if modalWidth > maxModalWidth {
		modalWidth = maxModalWidth
	}
	return modalWidth
}

// modalBoxStyle builds the style for the modal box.
//
// Expected:
//   - borderColor must be valid.
//   - int must be valid.
//   - float64 must be valid.
//
// Returns:
//   - A lipgloss.Style value.
//
// Side effects:
//   - None.
func (m *Modal) modalBoxStyle(borderColor lipgloss.Color, modalWidth int, opacity float64) lipgloss.Style {
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(modalWidth)
	if opacity < 1.0 {
		boxStyle = boxStyle.Faint(true)
	}
	return boxStyle
}

// calculateOpacity calculates the current opacity based on fade-in duration.
//
// Returns:
//   - A float64 between 0.0 and 1.0 representing current opacity.
//
// Side effects:
//   - None.
func (m *Modal) calculateOpacity() float64 {
	if m.FadeInDuration == 0 {
		return 1.0
	}

	elapsed := time.Since(m.fadeStartTime)
	if elapsed >= m.FadeInDuration {
		return 1.0
	}

	return float64(elapsed) / float64(m.FadeInDuration)
}

// getStyleForType returns border color and icon for the modal type.
//
// Returns:
//   - A lipgloss.Color for the border and a string icon for the modal type.
//
// Side effects:
//   - None.
func (m *Modal) getStyleForType() (lipgloss.Color, string) {
	theme := m.getTheme()

	switch m.Type {
	case ModalError:
		return theme.ErrorColor(), "⚠️"
	case ModalLoading:
		return theme.InfoColor(), "⏳"
	case ModalProgress:
		return theme.InfoColor(), "📊"
	case ModalSuccess:
		return theme.SuccessColor(), "✅"
	case ModalWarning:
		return theme.WarningColor(), "⚠️"
	default:
		return theme.BorderColor(), "ℹ️"
	}
}

// renderProgressBar renders a progress bar for the given width.
//
// Expected:
//   - width is a positive integer.
//
// Returns:
//   - A formatted progress bar string like "[████░░░░] 42%".
//
// Side effects:
//   - None.
func (m *Modal) renderProgressBar(width int, _ themes.Theme) string {
	percentage := int(m.Progress * 100)
	barWidth := width - 8

	filled := int(float64(barWidth) * m.Progress)
	if filled > barWidth {
		filled = barWidth
	}

	filledBar := strings.Repeat("█", filled)
	emptyBar := strings.Repeat("░", barWidth-filled)

	return fmt.Sprintf("[%s%s] %d%%", filledBar, emptyBar, percentage)
}

// wrapText wraps text to fit within the specified width.
//
// Expected:
//   - width is a positive integer.
//
// Returns:
//   - The text wrapped to fit within the given width.
//
// Side effects:
//   - None.
func wrapText(text string, width int) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}

	var lines []string
	var currentLine string

	for _, word := range words {
		testLine := currentLine
		if testLine != "" {
			testLine += " "
		}
		testLine += word

		if lipgloss.Width(testLine) <= width {
			currentLine = testLine
		} else {
			if currentLine != "" {
				lines = append(lines, currentLine)
			}
			currentLine = word
		}
	}

	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return strings.Join(lines, "\n")
}

// UpdateProgress updates the progress value (0.0 to 1.0).
//
// Expected:
//   - float64 must be valid.
//
// Side effects:
//   - None.
func (m *Modal) UpdateProgress(progress float64) {
	if progress < 0.0 {
		progress = 0.0
	}
	if progress > 1.0 {
		progress = 1.0
	}
	m.Progress = progress
}

// AdvanceSpinner advances the spinner to the next frame.
//
// Side effects:
//   - None.
func (m *Modal) AdvanceSpinner() {
	if m.spinner != nil {
		m.spinner.Advance()
	}
}

// RotateMessage advances to the next message if a rotator is set.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (m *Modal) RotateMessage() string {
	if m.messageRotator != nil {
		return m.messageRotator.Rotate()
	}
	return m.Message
}

// Init initializes the modal and starts spinner animation for loading modals
// or countdown tick for success modals with auto-dismiss.
//
// Returns:
//   - A tea.Cmd value.
//
// Side effects:
//   - None.
func (m *Modal) Init() tea.Cmd {
	if m.Type == ModalLoading && m.spinner != nil {
		return m.tickSpinner()
	}
	if m.Type == ModalSuccess && m.AutoDismiss > 0 {
		m.countdownRemaining = int(m.AutoDismiss.Seconds())
		return m.tickCountdown()
	}
	return nil
}

// Update handles messages for the modal, advancing the spinner on tick
// and handling countdown tick for success modals with auto-dismiss.
//
// Expected:
//   - msg must be valid.
//
// Returns:
//   - A tea.Cmd value.
//
// Side effects:
//   - None.
func (m *Modal) Update(msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case ModalSpinnerTickMsg:
		if m.Type == ModalLoading && m.spinner != nil {
			m.spinner.Advance()
			if m.messageRotator != nil {
				m.messageRotator.Rotate()
			}
			return m.tickSpinner()
		}
	case ModalCountdownTickMsg:
		if m.Type == ModalSuccess && m.countdownRemaining > 0 {
			m.countdownRemaining--
			if m.countdownRemaining <= 0 {
				return func() tea.Msg {
					return ModalAutoDismissMsg{}
				}
			}
			return m.tickCountdown()
		}
	}
	return nil
}

// tickSpinner returns a command to tick the spinner animation.
//
// Returns:
//   - A tea.Cmd that schedules a ModalSpinnerTickMsg.
//
// Side effects:
//   - None.
func (m *Modal) tickSpinner() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
		return ModalSpinnerTickMsg{}
	})
}

// tickCountdown returns a command to tick the auto-dismiss countdown.
//
// Returns:
//   - A tea.Cmd that schedules a ModalCountdownTickMsg.
//
// Side effects:
//   - None.
func (m *Modal) tickCountdown() tea.Cmd {
	return tea.Tick(1*time.Second, func(_ time.Time) tea.Msg {
		return ModalCountdownTickMsg{}
	})
}

// ============================================================================
// Modal Overlay System
// ============================================================================

const (
	// MinOverlayWidth is the minimum width for overlay modals.
	MinOverlayWidth = 40
	// MaxOverlayWidth is the maximum width for overlay modals.
	MaxOverlayWidth = 120
	// DefaultOverlayWidth is the default width for overlay modals.
	DefaultOverlayWidth = 60
)

// OverlayModal represents a modal that renders as an overlay on top of dimmed content.
// It supports configurable width, title, content, and footer.
type OverlayModal struct {
	Title   string
	Content string
	Footer  string
	Width   int
	theme   themes.Theme
}

// NewOverlayModal creates a new overlay modal with the given title and content.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized OverlayModal ready for use.
//
// Side effects:
//   - None.
func NewOverlayModal(title, content string) *OverlayModal {
	return &OverlayModal{
		Title:   title,
		Content: content,
		Width:   DefaultOverlayWidth,
		theme:   themes.NewDefaultTheme(),
	}
}

// getTheme returns the theme or default if nil.
//
// Returns:
//   - The assigned theme, or a default theme if none is set.
//
// Side effects:
//   - None.
func (o *OverlayModal) getTheme() themes.Theme {
	if o.theme != nil {
		return o.theme
	}
	return themes.NewDefaultTheme()
}

// WithTheme sets the theme for the overlay modal.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized OverlayModal ready for use.
//
// Side effects:
//   - None.
func (o *OverlayModal) WithTheme(theme themes.Theme) *OverlayModal {
	o.theme = theme
	return o
}

// SetWidth sets the modal width, clamping to min/max bounds.
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A fully initialized OverlayModal ready for use.
//
// Side effects:
//   - None.
func (o *OverlayModal) SetWidth(width int) *OverlayModal {
	if width < MinOverlayWidth {
		width = MinOverlayWidth
	}
	if width > MaxOverlayWidth {
		width = MaxOverlayWidth
	}
	o.Width = width
	return o
}

// SetFooter sets the footer text for the modal.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized OverlayModal ready for use.
//
// Side effects:
//   - None.
func (o *OverlayModal) SetFooter(footer string) *OverlayModal {
	o.Footer = footer
	return o
}

// RenderCentered renders the modal centered over the dimmed background.
//
// Expected:
//   - Must be a valid string.
//   - int must be valid.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (o *OverlayModal) RenderCentered(background string, termWidth, termHeight int) string {
	return RenderOverlay(background, o.buildContent(), termWidth, termHeight, o.getTheme())
}

// buildContent assembles the modal content with title, body, and footer.
//
// Returns:
//   - A rendered string combining title, content, and footer.
//
// Side effects:
//   - None.
func (o *OverlayModal) buildContent() string {
	theme := o.getTheme()
	var parts []string

	// Add title with styling
	if o.Title != "" {
		titleStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.PrimaryColor()).
			MarginBottom(1)
		parts = append(parts, titleStyle.Render(o.Title))
	}

	// Add content
	if o.Content != "" {
		parts = append(parts, o.Content)
	}

	// Add footer with muted styling
	if o.Footer != "" {
		footerStyle := lipgloss.NewStyle().
			Foreground(theme.MutedColor()).
			MarginTop(1)
		parts = append(parts, footerStyle.Render(o.Footer))
	}

	return strings.Join(parts, "\n")
}

// DimContent applies a dimmed/faded style to the content.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func DimContent(content string) string {
	if content == "" {
		return ""
	}

	dimStyle := lipgloss.NewStyle().Faint(true)
	return dimStyle.Render(content)
}

// RenderOverlay renders modal content centered over a dimmed background.
//
// Expected:
//   - Must be a valid string.
//   - int must be valid.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.

// calculateModalPosition computes the starting Y position and available height for the modal overlay.
//
// Expected:
//   - modalHeight, termHeight, and logoHeight are non-negative integers.
//
// Returns:
//   - startY: the vertical offset for the modal.
//   - availableHeight: the usable height for the modal content.
//
// Side effects:
//   - None.
func calculateModalPosition(modalHeight, termHeight, logoHeight int) (startY, availableHeight int) {
	startY = logoHeight + 1
	if termHeight < 15 {
		startY = (termHeight - modalHeight) / 2
		if startY < 0 {
			startY = 0
		}
	}
	availableHeight = termHeight - startY - 2
	if availableHeight <= 0 {
		availableHeight = termHeight - 2
		if availableHeight < 1 {
			availableHeight = termHeight
		}
		startY = 0
	}
	if modalHeight > availableHeight {
		needed := modalHeight - availableHeight
		reduction := needed
		if reduction > startY {
			reduction = startY
		}
		startY -= reduction
		availableHeight = termHeight - startY - 2
		if availableHeight < 1 {
			availableHeight = termHeight
			startY = 0
		}
	}
	return startY, availableHeight
}

// truncateModalLines truncates modal lines and adds a marker if needed.
//
// Expected:
//   - availableHeight is a positive integer.
//
// Returns:
//   - The truncated lines and updated modal height.
//
// Side effects:
//   - None.
func truncateModalLines(modalLines []string, modalHeight, availableHeight int) ([]string, int) {
	if modalHeight > availableHeight && availableHeight > 0 {
		if availableHeight <= len(modalLines) {
			modalLines = modalLines[:availableHeight]
			modalHeight = availableHeight
		}
		if modalHeight > 0 {
			lastLine := modalLines[modalHeight-1]
			modalLines[modalHeight-1] = lastLine + " ↓"
		}
	}
	return modalLines, modalHeight
}

// normalizeBackgroundLines ensures bgLines is exactly termHeight lines.
//
// Expected:
//   - termHeight is a positive integer.
//
// Returns:
//   - A slice of exactly termHeight lines, padded or truncated.
//
// Side effects:
//   - None.
func normalizeBackgroundLines(bgLines []string, termWidth, termHeight int) []string {
	for len(bgLines) < termHeight {
		bgLines = append(bgLines, strings.Repeat(" ", termWidth))
	}
	if len(bgLines) > termHeight {
		bgLines = bgLines[:termHeight]
	}
	return bgLines
}

// RenderOverlay renders a modal overlay on top of a background with dimming and centering.
//
// Expected:
//   - background: background content string
//   - modalContent: modal content to overlay
//   - termWidth: terminal width in characters
//   - termHeight: terminal height in characters
//   - theme: theme for styling (uses default if nil)
//
// Returns:
//   - string: rendered overlay output
//
// Side effects:
//   - None.
func RenderOverlay(background, modalContent string, termWidth, termHeight int, theme themes.Theme) string {
	if theme == nil {
		theme = themes.NewDefaultTheme()
	}

	// Dim the entire background first
	dimmedBg := DimContent(background)

	// Create modal box with border
	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderColor()).
		Padding(1, 2).
		MaxWidth(MaxOverlayWidth)

	modalBox := modalStyle.Render(modalContent)

	// Split both into lines
	bgLines := strings.Split(dimmedBg, "\n")
	modalLines := strings.Split(modalBox, "\n")

	logoHeight := display.DefaultLogoHeight
	startY, availableHeight := calculateModalPosition(len(modalLines), termHeight, logoHeight)
	modalLines, _ = truncateModalLines(modalLines, len(modalLines), availableHeight)
	bgLines = normalizeBackgroundLines(bgLines, termWidth, termHeight)

	// Create result by copying background
	result := make([]string, termHeight)
	copy(result, bgLines)

	// Overlay modal lines (centered horizontally) onto the background
	for i, modalLine := range modalLines {
		lineIndex := startY + i
		if lineIndex >= 0 && lineIndex < termHeight {
			centeredModalLine := lipgloss.PlaceHorizontal(termWidth, lipgloss.Center, modalLine)
			result[lineIndex] = centeredModalLine
		}
	}

	return strings.Join(result, "\n")
}

// RenderOverlayWithDefaultTheme renders modal content using the default theme.
//
// Expected:
//   - Must be a valid string.
//   - int must be valid.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func RenderOverlayWithDefaultTheme(background, modalContent string, termWidth, termHeight int) string {
	return RenderOverlay(background, modalContent, termWidth, termHeight, themes.NewDefaultTheme())
}
