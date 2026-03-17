// Package behaviors provides embeddable components for table-based UIs.
//
// # Overview
//
// The behaviors package offers composable, type-safe components that can be embedded in
// intents to eliminate boilerplate code for common UI patterns. Each behavior
// is designed to work independently or in composition with others.
//
// # Architecture
//
// Behaviors follow an embeddable pattern:
//
//	type MyIntent struct {
//	    *intents.BaseIntent
//	    *behaviors.TableBehavior[*domain.Item]
//	}
//
// # Available Behaviors
//
//   - TableBehavior[T]: Provides data binding, pagination, navigation, filtering, and sorting
//   - WizardBehavior[T]: Provides multi-step form wizard state management
//   - FilterBehavior: Interface for search/filter/sort functionality
//
// # Design Principles
//
//   - Type Safety: All behaviors use generics for type-safe item handling
//   - Single Source of Truth: TableBehavior owns the data
//   - No State Caching: Behaviors always delegate to the table for current data
//   - Embeddable: Behaviors are designed to be embedded in intent structs
//
// # Usage Example
//
//	// Create table behavior
//	table := behaviors.NewTableBehavior(
//	    theme,
//	    []behaviors.ColumnDef{
//	        {Title: "Name", Width: 25},
//	        {Title: "Status", Width: 15},
//	    },
//	    func(item *MyItem, idx int) []string {
//	        return []string{item.Name, item.Status}
//	    },
//	).PageSize(15).EmptyMessage("No items yet")
//
//	// In Update:
//	if table.HandleNavigation(keyStr) {
//	    return nil
//	}
//
//	// In View:
//	return table.Render()
//
// # Testing
//
// Each behavior is independently testable without requiring an intent:
//
//	table := behaviors.NewTableBehavior(...)
//	table.SetItems(testItems)
//	Expect(table.GetSelectedItem()).To(Equal(testItems[0]))
//
// For more details, see the individual behavior documentation.
package behaviors
