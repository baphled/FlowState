package behaviors

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/tui/navigation"
	"github.com/baphled/flowstate/internal/tui/themes"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
)

// TableBehavior[T] provides data binding, pagination, navigation, filtering, and sorting
// for table-based list views. It implements the ListNavigator interface and can be
// embedded in intents to eliminate boilerplate table management code.
//
// Usage:
//
//	table := behaviors.NewTableBehavior(theme, columns, formatter).
//	    PageSize(20).
//	    EmptyMessage("No items found")
//	table.SetItems(myItems)
//
//	// In Update:
//	if table.HandleNavigation(keyStr) {
//	    return nil
//	}
//
//	// In View:
//	return table.Render()
type TableBehavior[T any] struct {
	theme.Aware

	columns      []ColumnDef
	rowFormatter RowFormatter[T]
	pageSize     int

	allItems      []T
	displayItems  []T
	selectedIndex int

	filterPredicate FilterPredicate[T]
	sortComparator  SortComparator[T]
	sortReverse     bool

	emptyMessage     string
	paginationPrefix string
	showPagination   bool

	table         table.Model
	viewport      viewport.Model
	useViewport   bool
	navHandler    *navigation.ListNavigationHandler
	width, height int
	needsRefresh  bool
}

// NewTableBehavior creates a new table behavior with the given configuration.
//
// Expected:
//   - themeObj must be a valid theme instance.
//   - columns must define at least one column.
//   - formatter must be a non-nil function that converts items to row cells.
//
// Returns:
//   - A fully initialized TableBehavior ready for use.
//
// Side effects:
//   - None.
func NewTableBehavior[T any](themeObj themes.Theme, columns []ColumnDef, formatter RowFormatter[T]) *TableBehavior[T] {
	bubbleColumns := make([]table.Column, len(columns))
	for i, col := range columns {
		bubbleColumns[i] = table.Column{
			Title: col.Title,
			Width: col.Width,
		}
	}

	bubblesTable := table.New(
		table.WithColumns(bubbleColumns),
		table.WithRows([]table.Row{}),
		table.WithFocused(true),
		table.WithHeight(15),
	)

	behavior := &TableBehavior[T]{
		columns:          columns,
		rowFormatter:     formatter,
		pageSize:         15,
		emptyMessage:     "No items to display",
		paginationPrefix: "Items",
		showPagination:   true,
		table:            bubblesTable,
		width:            100,
		height:           24,
		allItems:         []T{},
		displayItems:     []T{},
		selectedIndex:    0,
	}

	behavior.SetTheme(themeObj)
	behavior.navHandler = navigation.NewListNavigationHandler(behavior)

	return behavior
}

// PageSize configures the number of items shown per page for pagination.
//
// Expected:
//   - size must be a positive integer.
//
// Returns:
//   - The TableBehavior for method chaining.
//
// Side effects:
//   - Updates the internal page size.
func (tb *TableBehavior[T]) PageSize(size int) *TableBehavior[T] {
	tb.pageSize = size
	return tb
}

// EmptyMessage configures the placeholder text displayed when the table is empty.
//
// Expected:
//   - msg should be a non-empty string describing the empty state.
//
// Returns:
//   - The TableBehavior for method chaining.
//
// Side effects:
//   - Updates the internal empty message.
func (tb *TableBehavior[T]) EmptyMessage(msg string) *TableBehavior[T] {
	tb.emptyMessage = msg
	return tb
}

// PaginationPrefix configures the label shown before item counts in pagination.
//
// Expected:
//   - prefix should be a non-empty string label for the item type.
//
// Returns:
//   - The TableBehavior for method chaining.
//
// Side effects:
//   - Updates the internal pagination prefix.
func (tb *TableBehavior[T]) PaginationPrefix(prefix string) *TableBehavior[T] {
	tb.paginationPrefix = prefix
	return tb
}

// Dimensions configures the rendering area for the table and its internal components.
//
// Expected:
//   - width and height must be positive integers.
//
// Returns:
//   - The TableBehavior for method chaining.
//
// Side effects:
//   - Updates the internal width, height, and underlying table dimensions.
func (tb *TableBehavior[T]) Dimensions(width, height int) *TableBehavior[T] {
	tb.width = width
	tb.height = height
	tb.table.SetWidth(width)
	tb.table.SetHeight(height - 10)
	return tb
}

// SetHeight configures the table to use viewport with the specified height.
// This enables scrolling when content exceeds the available height.
//
// Expected:
//   - height must be a positive integer representing available content height.
//
// Returns:
//   - The TableBehavior for method chaining.
//
// Side effects:
//   - Updates the internal height, enables viewport mode, and reconfigures dimensions.
func (tb *TableBehavior[T]) SetHeight(height int) *TableBehavior[T] {
	tb.height = height
	tb.useViewport = true

	tableHeight := height - 3
	if tableHeight < 5 {
		tableHeight = 5
	}
	tb.table.SetHeight(tableHeight)

	if tb.viewport.Width == 0 {
		tb.viewport = viewport.New(tb.width, height)
	} else {
		tb.viewport.Width = tb.width
		tb.viewport.Height = height
	}

	return tb
}

// HidePagination disables the pagination footer below the table.
//
// Returns:
//   - The TableBehavior for method chaining.
//
// Side effects:
//   - Disables pagination display.
func (tb *TableBehavior[T]) HidePagination() *TableBehavior[T] {
	tb.showPagination = false
	return tb
}

// ShowPagination enables the pagination footer below the table.
//
// Returns:
//   - The TableBehavior for method chaining.
//
// Side effects:
//   - Enables pagination display.
func (tb *TableBehavior[T]) ShowPagination() *TableBehavior[T] {
	tb.showPagination = true
	return tb
}

// SetItems replaces the data source and triggers a full refresh of the display list.
//
// Expected:
//   - items may be nil, which is treated as an empty slice.
//
// Returns:
//   - The TableBehavior for method chaining.
//
// Side effects:
//   - Updates the internal item list, resets selection to index zero, and triggers a display refresh.
func (tb *TableBehavior[T]) SetItems(items []T) *TableBehavior[T] {
	if items == nil {
		items = []T{}
	}
	tb.allItems = items
	tb.selectedIndex = 0
	tb.needsRefresh = true
	tb.refreshDisplayItems()
	return tb
}

// GetItems retrieves the filtered and sorted item list for rendering.
//
// Returns:
//   - The current display items slice.
//
// Side effects:
//   - May trigger a display refresh if needed.
func (tb *TableBehavior[T]) GetItems() []T {
	tb.refreshDisplayItems()
	return tb.displayItems
}

// GetSelectedItem retrieves the item at the current cursor position within the filtered list.
//
// Returns:
//   - A pointer to the selected item, or nil if none.
//
// Side effects:
//   - May trigger a display refresh if needed.
func (tb *TableBehavior[T]) GetSelectedItem() *T {
	tb.refreshDisplayItems()
	if len(tb.displayItems) == 0 {
		return nil
	}
	if tb.selectedIndex < 0 || tb.selectedIndex >= len(tb.displayItems) {
		return nil
	}
	return &tb.displayItems[tb.selectedIndex]
}

// GetSelectedIndex reports the cursor position within the display list.
//
// Returns:
//   - The current selection index.
//
// Side effects:
//   - None.
func (tb *TableBehavior[T]) GetSelectedIndex() int {
	return tb.selectedIndex
}

// IsEmpty checks whether the table has any items after filtering.
//
// Returns:
//   - True if there are no display items.
//
// Side effects:
//   - May trigger a display refresh if needed.
func (tb *TableBehavior[T]) IsEmpty() bool {
	tb.refreshDisplayItems()
	return len(tb.displayItems) == 0
}

// Count reports the size of the filtered item list.
//
// Returns:
//   - The number of display items.
//
// Side effects:
//   - May trigger a display refresh if needed.
func (tb *TableBehavior[T]) Count() int {
	tb.refreshDisplayItems()
	return len(tb.displayItems)
}

// TotalCount reports the size of the original unfiltered data set.
//
// Returns:
//   - The total number of items.
//
// Side effects:
//   - None.
func (tb *TableBehavior[T]) TotalCount() int {
	return len(tb.allItems)
}

// GetTotalItems implements ListNavigator.
//
// Returns:
//   - The count of display items.
//
// Side effects:
//   - None.
func (tb *TableBehavior[T]) GetTotalItems() int {
	return tb.Count()
}

// SetSelectedIndex implements ListNavigator.
//
// Expected:
//   - idx is the new selection index.
//
// Side effects:
//   - Updates the selection and syncs the table display.
func (tb *TableBehavior[T]) SetSelectedIndex(idx int) {
	tb.refreshDisplayItems()

	if len(tb.displayItems) == 0 {
		tb.selectedIndex = 0
		return
	}

	if idx < 0 {
		idx = 0
	}
	if idx >= len(tb.displayItems) {
		idx = len(tb.displayItems) - 1
	}

	tb.selectedIndex = idx
	tb.updateTableRows()

	if tb.useViewport && tb.viewport.Height > 0 {
		rowHeight := 1
		selectedLinePosition := idx * rowHeight

		if selectedLinePosition >= tb.viewport.YOffset+tb.viewport.Height {
			tb.viewport.SetYOffset(selectedLinePosition - tb.viewport.Height + 1)
		}

		if selectedLinePosition < tb.viewport.YOffset {
			tb.viewport.SetYOffset(selectedLinePosition)
		}
	}
}

// GetPageSize implements ListNavigator.
//
// Returns:
//   - The page size.
//
// Side effects:
//   - None.
func (tb *TableBehavior[T]) GetPageSize() int {
	return tb.pageSize
}

// HandleNavigation processes navigation keys (up, down, j, k, pgup, pgdn, home, end, g, G).
// Returns true if the key was handled.
//
// Expected:
//   - keyStr must be a recognized navigation key string.
//
// Returns:
//   - True if the key was recognized and handled, false otherwise.
//
// Side effects:
//   - Updates the selected index via the internal navigation handler when a key is handled.
func (tb *TableBehavior[T]) HandleNavigation(keyStr string) bool {
	if len(tb.displayItems) == 0 {
		return false
	}
	return tb.navHandler.HandleKey(keyStr)
}

// SetFilter applies a filter predicate.
// Pass nil to clear the filter.
// Selection is preserved if the previously selected item passes the filter.
//
// Expected:
//   - pred may be nil to clear the filter, or a function that returns true for items to include.
//
// Returns:
//   - The TableBehavior for method chaining.
//
// Side effects:
//   - Updates the internal filter predicate, triggers a display refresh, and attempts to preserve selection.
func (tb *TableBehavior[T]) SetFilter(pred FilterPredicate[T]) *TableBehavior[T] {
	var selectedItem *T
	if tb.selectedIndex >= 0 && tb.selectedIndex < len(tb.displayItems) {
		selectedItem = &tb.displayItems[tb.selectedIndex]
	}

	tb.filterPredicate = pred
	tb.needsRefresh = true
	tb.refreshDisplayItems()

	if selectedItem != nil {
		for i, item := range tb.displayItems {
			if compareItems(*selectedItem, item) {
				tb.selectedIndex = i
				tb.updateTableRows()
				return tb
			}
		}
	}

	tb.selectedIndex = 0
	tb.updateTableRows()
	return tb
}

// ClearFilter deactivates filtering and restores the full item set.
//
// Returns:
//   - The TableBehavior for method chaining.
//
// Side effects:
//   - Clears the filter and refreshes the display.
func (tb *TableBehavior[T]) ClearFilter() *TableBehavior[T] {
	tb.filterPredicate = nil
	tb.needsRefresh = true
	tb.refreshDisplayItems()
	tb.updateTableRows()
	return tb
}

// HasFilter checks whether a filter predicate is currently applied.
//
// Returns:
//   - True if a filter is active.
//
// Side effects:
//   - None.
func (tb *TableBehavior[T]) HasFilter() bool {
	return tb.filterPredicate != nil
}

// SetSort applies a sort comparator.
// Pass nil to use the original order.
//
// Expected:
//   - cmp may be nil to clear sorting, or a comparator function.
//   - reverse controls whether the sort order is inverted.
//
// Returns:
//   - The TableBehavior for method chaining.
//
// Side effects:
//   - Updates the internal sort comparator and direction, triggers a display refresh.
func (tb *TableBehavior[T]) SetSort(cmp SortComparator[T], reverse bool) *TableBehavior[T] {
	var selectedItem *T
	if tb.selectedIndex >= 0 && tb.selectedIndex < len(tb.displayItems) {
		selectedItem = &tb.displayItems[tb.selectedIndex]
	}

	tb.sortComparator = cmp
	tb.sortReverse = reverse
	tb.needsRefresh = true
	tb.refreshDisplayItems()

	if selectedItem != nil {
		for i, item := range tb.displayItems {
			if compareItems(*selectedItem, item) {
				tb.selectedIndex = i
				tb.updateTableRows()
				return tb
			}
		}
	}

	tb.updateTableRows()
	return tb
}

// ClearSort deactivates sorting and restores the original item order.
//
// Returns:
//   - The TableBehavior for method chaining.
//
// Side effects:
//   - Clears the sort and refreshes the display.
func (tb *TableBehavior[T]) ClearSort() *TableBehavior[T] {
	tb.sortComparator = nil
	tb.sortReverse = false
	tb.needsRefresh = true
	tb.refreshDisplayItems()
	tb.updateTableRows()
	return tb
}

// HasSort checks whether a sort comparator is currently applied.
//
// Returns:
//   - True if a sort is active.
//
// Side effects:
//   - None.
func (tb *TableBehavior[T]) HasSort() bool {
	return tb.sortComparator != nil
}

// Render produces the complete table view including rows, selection indicator, and pagination.
//
// Returns:
//   - The rendered table view string.
//
// Side effects:
//   - May trigger a display refresh if needed.
func (tb *TableBehavior[T]) Render() string {
	tb.refreshDisplayItems()

	if len(tb.displayItems) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(tb.MutedColor()).
			Italic(true)
		emptyContent := emptyStyle.Render(tb.emptyMessage)

		parts := []string{emptyContent}

		if tb.showPagination {
			parts = append(parts, "", tb.RenderPaginationInfo())
		}

		return lipgloss.JoinVertical(lipgloss.Left, parts...)
	}

	tb.updateTableRows()

	tableView := tb.table.View()
	parts := []string{tableView}

	if tb.showPagination {
		parts = append(parts, "", tb.RenderPaginationInfo())
	}

	combined := lipgloss.JoinVertical(lipgloss.Left, parts...)

	if tb.useViewport && tb.viewport.Width > 0 && tb.viewport.Height > 0 {
		tb.viewport.SetContent(combined)
		return tb.viewport.View()
	}

	return combined
}

// RenderPaginationInfo produces the formatted item count and page position footer.
//
// Returns:
//   - The pagination info string.
//
// Side effects:
//   - May trigger a display refresh if needed.
func (tb *TableBehavior[T]) RenderPaginationInfo() string {
	tb.refreshDisplayItems()

	totalItems := len(tb.displayItems)
	if totalItems == 0 {
		return tb.paginationPrefix + ": 0"
	}

	currentPage := (tb.selectedIndex / tb.pageSize) + 1
	totalPages := (totalItems + tb.pageSize - 1) / tb.pageSize

	return fmt.Sprintf("%s: %d | Page %d of %d", tb.paginationPrefix, totalItems, currentPage, totalPages)
}

func (tb *TableBehavior[T]) refreshDisplayItems() {
	if !tb.needsRefresh {
		return
	}

	result := make([]T, len(tb.allItems))
	copy(result, tb.allItems)

	if tb.filterPredicate != nil {
		filtered := make([]T, 0, len(result))
		for _, item := range result {
			if tb.filterPredicate(item) {
				filtered = append(filtered, item)
			}
		}
		result = filtered
	}

	if tb.sortComparator != nil {
		sort.Slice(result, func(i, j int) bool {
			cmp := tb.sortComparator(result[i], result[j])
			if tb.sortReverse {
				return cmp > 0
			}
			return cmp < 0
		})
	}

	tb.displayItems = result
	tb.needsRefresh = false

	if tb.selectedIndex >= len(tb.displayItems) {
		if len(tb.displayItems) > 0 {
			tb.selectedIndex = len(tb.displayItems) - 1
		} else {
			tb.selectedIndex = 0
		}
	}
}

func (tb *TableBehavior[T]) updateTableRows() {
	if len(tb.displayItems) == 0 {
		tb.table.SetRows([]table.Row{})
		return
	}

	var start, end int
	if tb.useViewport {
		start = 0
		end = len(tb.displayItems)
	} else {
		page := tb.selectedIndex / tb.pageSize
		start = page * tb.pageSize
		end = start + tb.pageSize
		if end > len(tb.displayItems) {
			end = len(tb.displayItems)
		}
	}

	pageItems := tb.displayItems[start:end]

	rows := make([]table.Row, len(pageItems))
	for i, item := range pageItems {
		realIdx := start + i
		cells := tb.rowFormatter(item, realIdx)

		if realIdx == tb.selectedIndex {
			cells[0] = tb.navHandler.FormatRowText(realIdx, cells[0])
		} else {
			cells[0] = "  " + cells[0]
		}

		rows[i] = table.Row(cells)
	}

	tb.table.SetRows(rows)

	relativeCursor := tb.selectedIndex - start
	if relativeCursor < 0 {
		relativeCursor = 0
	}
	if relativeCursor >= len(rows) {
		relativeCursor = len(rows) - 1
	}
	tb.table.SetCursor(relativeCursor)
}

func compareItems[T any](a, b T) bool {
	return reflect.DeepEqual(a, b)
}
