package widgets

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// todoItem mirrors todo.Item for JSON decoding without creating a cross-layer dependency.
type todoItem struct {
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
}

// FormatTodoList parses a JSON todo list and returns a styled terminal string.
//
// Expected:
//   - jsonStr is a JSON array of todo items, each with content, status, and priority fields.
//
// Returns:
//   - A styled multi-line string showing the todo header, active count, and each item with status icon and priority badge.
//   - "📋 todos updated" if the JSON cannot be parsed.
//   - "📋 Todo list cleared" if the array is empty.
//
// Side effects:
//   - None.
func FormatTodoList(jsonStr string) string {
	var items []todoItem
	if err := json.Unmarshal([]byte(jsonStr), &items); err != nil {
		return "📋 todos updated"
	}
	if len(items) == 0 {
		return "📋 Todo list cleared"
	}

	var sb strings.Builder
	active := countActiveItems(items)

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))
	sb.WriteString(headerStyle.Render("📋 Todos"))
	if active > 0 {
		countStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		sb.WriteString(countStyle.Render(" (" + strconv.Itoa(active) + " active)"))
	}
	sb.WriteString("\n")

	for _, item := range items {
		sb.WriteString("  ")
		sb.WriteString(statusIcon(item.Status))
		sb.WriteString(" ")
		sb.WriteString(priorityBadge(item.Priority))
		sb.WriteString(" ")

		contentStyle := lipgloss.NewStyle()
		if item.Status == "completed" || item.Status == "cancelled" {
			contentStyle = contentStyle.Foreground(lipgloss.Color("240")).Strikethrough(true)
		}
		sb.WriteString(contentStyle.Render(item.Content))
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}

// countActiveItems returns the number of items that are neither completed nor cancelled.
//
// Expected:
//   - items is a non-nil slice of todoItem values.
//
// Returns:
//   - The count of items whose status is not "completed" or "cancelled".
//
// Side effects:
//   - None.
func countActiveItems(items []todoItem) int {
	active := 0
	for _, item := range items {
		if item.Status != "completed" && item.Status != "cancelled" {
			active++
		}
	}
	return active
}

// statusIcon returns a styled Unicode icon for the given todo status.
//
// Expected:
//   - status is one of "in_progress", "completed", "cancelled", or any other value for pending.
//
// Returns:
//   - A Lip Gloss styled string containing a single character icon.
//
// Side effects:
//   - None.
func statusIcon(status string) string {
	switch status {
	case "in_progress":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Render("▶")
	case "completed":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render("✓")
	case "cancelled":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("✗")
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Render("○")
	}
}

// priorityBadge returns a styled priority label for the given priority level.
//
// Expected:
//   - priority is one of "high", "medium", or any other value treated as low.
//
// Returns:
//   - A Lip Gloss styled string such as "[H]", "[M]", or "[L]".
//
// Side effects:
//   - None.
func priorityBadge(priority string) string {
	switch priority {
	case "high":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("[H]")
	case "medium":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Render("[M]")
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("[L]")
	}
}
