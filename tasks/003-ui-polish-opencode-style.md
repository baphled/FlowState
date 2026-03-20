# Task 003: UI Polish - OpenCode Style

**Status:** Complete
**Priority:** High
**Depends On:** 002-basic-tui-shell

## Objective

Improve the TUI appearance to match OpenCode's look and feel with proper layout, styling, and user experience.

## Current State

The basic TUI works but needs polish:
- ❌ Basic header/footer without proper styling
- ❌ Simple message display without rich formatting
- ❌ No status bar or context information
- ❌ Inconsistent colors and spacing
- ❌ No theme system

## Target State

Professional TUI matching OpenCode aesthetics:
- ✅ Clean header with branding and context
- ✅ Status bar showing mode, model, and session info
- ✅ Rich message formatting with syntax highlighting
- ✅ Proper color scheme and spacing
- ✅ Theme system for consistent styling

## Deliverables

### Layout Components
- [ ] Header component with app title and breadcrumbs
- [ ] Footer component with help hints
- [ ] Status bar component (mode, model, session)
- [ ] Sidebar/panel structure (for future task panel)

### Styling System
- [ ] Theme interface and default theme
- [ ] Color palette matching OpenCode
- [ ] Typography styles (title, body, code, muted)
- [ ] Border styles and box components

### Message Display
- [ ] User message styling (different from AI)
- [ ] AI response styling with streaming indicator
- [ ] Code block rendering
- [ ] Markdown support (bold, italic, lists)
- [ ] Timestamp display (optional)

### Status Information
- [ ] Current mode indicator (INSERT/NORMAL)
- [ ] Active model display
- [ ] Token count or message count
- [ ] Connection status (when real Ollama integrated)

## Implementation Steps

### Step 1: Create Theme System
```go
// internal/tui/theme/theme.go
type Theme interface {
    Primary() lipgloss.Color
    Secondary() lipgloss.Color
    Background() lipgloss.Color
    Foreground() lipgloss.Color
    Muted() lipgloss.Color
    Error() lipgloss.Color
    Success() lipgloss.Color
    Border() lipgloss.Style
}
```

### Step 2: Build Layout Components
- Header with app name and version
- Footer with keybinding hints
- Status bar with context info

### Step 3: Improve Message Rendering
- Distinct user/AI message styles
- Better spacing and padding
- Streaming indicator while receiving

### Step 4: Add Help System
- Footer shows contextual help based on mode
- `?` key shows full help modal (future)

## Acceptance Criteria

- [ ] UI matches OpenCode's aesthetic quality
- [ ] Clear visual hierarchy
- [ ] Mode switching is obvious
- [ ] Messages are easy to distinguish (user vs AI)
- [ ] Consistent spacing and alignment
- [ ] Help hints guide the user
- [ ] Theme can be easily customized

## Design Reference

Study OpenCode's TUI for:
- Color scheme
- Layout proportions
- Status bar format
- Message display style
- Help text placement

## Testing

```bash
# Visual testing
make run

# Verify with different terminal sizes
# Test mode switching visual feedback
# Check message readability
```

## Files to Modify

```
internal/tui/
├── theme/
│   ├── theme.go          # Theme interface
│   └── default.go        # Default theme
├── components/
│   ├── header.go         # Header component
│   ├── footer.go         # Footer component
│   ├── status.go         # Status bar
│   └── message.go        # Message display
└── app/
    └── app.go            # Update to use new components
```

## Related Tasks

- Task 004: Real Ollama integration (will need status for connection)
- Task 005: Task panel (will use theme and layout)
- Task 006: Session management (will show in status bar)

## Notes

- Keep it simple - don't over-engineer
- Focus on readability and usability
- Use existing lipgloss styles from Charm ecosystem
- Reference Charm's TUI examples for inspiration
