// Package widgets provides higher-level composite components that combine
// primitives and behaviors into reusable UI patterns.
package widgets

import (
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/charmbracelet/lipgloss"
)

// itemType represents the type of content item in a detail view.
type itemType int

const (
	itemField itemType = iota
	itemSection
	itemList
	itemBulletList
)

// item represents a single piece of content in the detail view.
type item struct {
	kind      itemType
	label     string
	value     string
	values    []string
	separator string
}

// DetailView renders structured key-value detail information with consistent
// styling, text wrapping, sections, and list support.
//
// Usage:
//
//	dv := widgets.NewDetailView(theme).
//	    Title("Event Details").
//	    Width(60).
//	    Section("Basic Info").
//	    Field("Name", event.Name).
//	    FieldIf("Company", event.Company).  // Only if not empty
//	    List("Tags", event.Tags).
//	    Section("Metadata").
//	    Field("Created", event.CreatedAt.Format("2006-01-02"))
//	rendered := dv.Render()
type DetailView struct {
	theme.Aware

	title string
	width int
	items []item
}

// NewDetailView creates a new DetailView with the given theme.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized DetailView ready for use.
//
// Side effects:
//   - None.
func NewDetailView(th theme.Theme) *DetailView {
	dv := &DetailView{
		width: 0,
		items: make([]item, 0),
	}
	if th != nil {
		dv.SetTheme(th)
	}
	return dv
}

// Title sets the title displayed at the top of the detail view.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized DetailView ready for use.
//
// Side effects:
//   - None.
func (dv *DetailView) Title(title string) *DetailView {
	dv.title = title
	return dv
}

// Width sets the maximum width for text wrapping.
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A fully initialized DetailView ready for use.
//
// Side effects:
//   - None.
func (dv *DetailView) Width(width int) *DetailView {
	dv.width = width
	return dv
}

// Section starts a new section with the given title.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized DetailView ready for use.
//
// Side effects:
//   - None.
func (dv *DetailView) Section(title string) *DetailView {
	dv.items = append(dv.items, item{
		kind:  itemSection,
		label: title,
	})
	return dv
}

// Field adds a labeled field with a value.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized DetailView ready for use.
//
// Side effects:
//   - None.
func (dv *DetailView) Field(label, value string) *DetailView {
	dv.items = append(dv.items, item{
		kind:  itemField,
		label: label,
		value: value,
	})
	return dv
}

// FieldIf adds a field only if the value is not empty.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized DetailView ready for use.
//
// Side effects:
//   - None.
func (dv *DetailView) FieldIf(label, value string) *DetailView {
	if value != "" {
		return dv.Field(label, value)
	}
	return dv
}

// List adds a field with a list of string values.
//
// Expected:
//   - Must be a valid string.
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized DetailView ready for use.
//
// Side effects:
//   - None.
func (dv *DetailView) List(label string, values []string) *DetailView {
	dv.items = append(dv.items, item{
		kind:      itemList,
		label:     label,
		values:    values,
		separator: ", ",
	})
	return dv
}

// ListIf adds a list field only if the values slice is not empty.
//
// Expected:
//   - Must be a valid string.
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized DetailView ready for use.
//
// Side effects:
//   - None.
func (dv *DetailView) ListIf(label string, values []string) *DetailView {
	if len(values) > 0 {
		return dv.List(label, values)
	}
	return dv
}

// ListWithSeparator adds a list with a custom separator.
//
// Expected:
//   - Must be a valid string.
//   - Must be a valid string.
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized DetailView ready for use.
//
// Side effects:
//   - None.
func (dv *DetailView) ListWithSeparator(label string, values []string, separator string) *DetailView {
	dv.items = append(dv.items, item{
		kind:      itemList,
		label:     label,
		values:    values,
		separator: separator,
	})
	return dv
}

// BulletList adds a bulleted list.
//
// Expected:
//   - Must be a valid string.
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized DetailView ready for use.
//
// Side effects:
//   - None.
func (dv *DetailView) BulletList(label string, values []string) *DetailView {
	dv.items = append(dv.items, item{
		kind:   itemBulletList,
		label:  label,
		values: values,
	})
	return dv
}

// Render returns the rendered detail view as a string.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (dv *DetailView) Render() string {
	if len(dv.items) == 0 && dv.title == "" {
		return ""
	}

	var parts []string

	if dv.title != "" {
		titleStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(dv.PrimaryColor())
		parts = append(parts, titleStyle.Render(dv.title), "")
	}

	lastWasSection := false
	firstItem := true

	for _, itm := range dv.items {
		switch itm.kind {
		case itemSection:
			if !firstItem && !lastWasSection {
				parts = append(parts, "")
			}
			parts = append(parts, dv.renderSection(itm.label))
			lastWasSection = true

		case itemField:
			parts = append(parts, dv.renderField(itm.label, itm.value))
			lastWasSection = false

		case itemList:
			if len(itm.values) > 0 {
				value := strings.Join(itm.values, itm.separator)
				parts = append(parts, dv.renderField(itm.label, value))
			}
			lastWasSection = false

		case itemBulletList:
			parts = append(parts, dv.renderBulletList(itm.label, itm.values))
			lastWasSection = false
		}
		firstItem = false
	}

	return strings.Join(parts, "\n")
}

// renderSection renders a section header.
//
// Expected:
//   - title is non-empty.
//
// Returns:
//   - A styled section header string.
//
// Side effects:
//   - None.
func (dv *DetailView) renderSection(title string) string {
	sectionStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(dv.SecondaryColor())

	return sectionStyle.Render(title)
}

// renderField renders a label-value pair.
//
// Expected:
//   - label and value are non-empty strings.
//
// Returns:
//   - A formatted "Label: value" string, with wrapping if width is set.
//
// Side effects:
//   - None.
func (dv *DetailView) renderField(label, value string) string {
	labelStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(dv.MutedColor())

	displayValue := value
	if dv.width > 0 {
		displayValue = dv.wrapText(value, dv.width-len(label)-2)
	}

	if strings.Contains(displayValue, "\n") {
		lines := strings.Split(displayValue, "\n")
		var b strings.Builder
		b.WriteString(labelStyle.Render(label+":") + " " + lines[0])
		indent := strings.Repeat(" ", len(label)+2)
		for _, line := range lines[1:] {
			b.WriteString("\n" + indent + line)
		}
		return b.String()
	}

	return labelStyle.Render(label+":") + " " + displayValue
}

// renderBulletList renders a bulleted list.
//
// Expected:
//   - values contains the list items to render.
//
// Returns:
//   - A formatted string with a label header and bulleted items.
//
// Side effects:
//   - None.
func (dv *DetailView) renderBulletList(label string, values []string) string {
	labelStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(dv.MutedColor())

	var parts []string
	parts = append(parts, labelStyle.Render(label+":"))

	for _, v := range values {
		parts = append(parts, "  • "+v)
	}

	return strings.Join(parts, "\n")
}

// wrapWords wraps a slice of words into lines that fit within the specified width.
//
// Expected:
//   - width is a positive integer.
//
// Returns:
//   - A slice of wrapped lines.
//
// Side effects:
//   - None.
func wrapWords(words []string, width int) []string {
	var lines []string
	var currentLine strings.Builder

	for _, word := range words {
		if len(word) > width {
			if currentLine.Len() > 0 {
				lines = append(lines, currentLine.String())
				currentLine.Reset()
			}
			lines = append(lines, word)
			continue
		}

		spaceNeeded := len(word)
		if currentLine.Len() > 0 {
			spaceNeeded++
		}

		if currentLine.Len()+spaceNeeded > width {
			lines = append(lines, currentLine.String())
			currentLine.Reset()
		}

		if currentLine.Len() > 0 {
			currentLine.WriteString(" ")
		}
		currentLine.WriteString(word)
	}

	if currentLine.Len() > 0 {
		lines = append(lines, currentLine.String())
	}

	return lines
}

// wrapText wraps text to fit within the specified width.
//
// Expected:
//   - width is a positive integer; values below 10 are clamped to 10.
//
// Returns:
//   - The text wrapped to the given width.
//
// Side effects:
//   - None.
func (dv *DetailView) wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}
	if width < 10 {
		width = 10
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}

	lines := wrapWords(words, width)
	if len(lines) == 0 {
		return text
	}

	return strings.Join(lines, "\n")
}
