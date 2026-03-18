// Package widgets provides higher-level composite components and content rendering abstractions for intents via the View pattern.
//
// # Overview
//
// The View abstraction replaces Screen. A View handles only the content section inside an intent;
// the intent owns chrome (breadcrumbs, logo, footer) via ScreenLayout.
// Views are independently testable and composable.
//
// # Architecture
//
// The dependency rule: widgets/ imports only domain/ and uikit/. Never views/, intents/, or services/.
// The intent calls view.RenderContent() → ScreenLayout.WithContent() and
// view.HelpText() → ScreenLayout.WithHelp().
//
// Views follow an embeddable pattern:
//
//	type MyIntent struct {
//	    *intents.BaseIntent
//	    activeView widgets.View
//	}
//
// # View Types
//
//   - View: Interface for content rendering and help text
//   - ViewResult: Interface for intent communication
//   - Module: Interface for context-specific rendering (e.g., modals)
//   - BaseView: Embeddable struct for common view functionality
//   - NavigateViewResult: Navigate to another view
//   - CancelViewResult: Cancel the current workflow
//   - SubmitViewResult: Submit data and complete workflow
//   - ErrorViewResult: Report an error to the intent
//
// # Component Pattern
//
// List and form views are treated as domain-specific components within their own sub-packages
// (e.g., widgets/event/). This ensures consistency across different domain types like events, bursts, or skills.
//
// Components follow these conventions:
//
//   - Directory structure: Each domain type has its own directory:
//     widgets/event/, widgets/burst/, widgets/skill/, etc.
//   - Key bindings: Components use standard navigation keymaps from navigation.DefaultListKeyMap() and navigation.DefaultGlobalKeyMap().
//   - Input handling: Components use key.Matches() for reliable key handling logic.
//   - Help footer: Components use primitives.RenderHelpFooter() to maintain a consistent help text appearance across the application.
//
// This pattern allows for reusable list components where each list type receives an entity
// and the base component handles the standard behaviour.
//
// # Usage Example
//
// An intent uses a View like this:
//
//	func (i *MyIntent) View() string {
//	    layout := intents.CreateStandardViewWithBreadcrumbs(i.BaseIntent, "Main", "Feature")
//	    layout.WithContent(i.activeView.RenderContent())
//	    layout.WithHelp(i.activeView.HelpText())
//	    return layout.Render()
//	}
//
// # Module Usage
//
// A Module overrides a View for modal context:
//
//	type ModalModule struct{}
//	func (m *ModalModule) RenderContent(v widgets.View) string { /* modal-sized content */ }
//	func (m *ModalModule) HelpText(v widgets.View) string      { /* modal key bindings */ }
//
// # Migration Note
//
// View replaces Screen. New code must use View. Existing screens are being migrated to the View abstraction.
//
// # Testing
//
// Views are independently testable without an intent. Use fakeView test doubles implementing the View interface:
//
//	type fakeView struct {
//	    content string
//	    help    string
//	}
//	func (f *fakeView) RenderContent() string { return f.content }
//	func (f *fakeView) HelpText() string      { return f.help }
//
// # DetailView Widget
//
// DetailView renders structured key-value detail information with consistent styling, text wrapping,
// sections, and list support. It solves the problem of inconsistent detail screen rendering across the application.
//
// ## Features
//
//   - Field-value pairs with styled labels
//   - Automatic text wrapping when width is set
//   - Section headers for organising related fields
//   - List rendering (comma-separated, bulleted, custom separator)
//   - Conditional fields/lists (only show if non-empty)
//   - Theme-aware styling via theme.Aware embedding
//
// ## Usage
//
// Basic usage with fields:
//
//	dv := widgets.NewDetailView(theme).
//	    Title("Event Details").
//	    Width(60).
//	    Field("Name", event.Name).
//	    Field("Date", event.Date.Format("2006-01-02")).
//	    FieldIf("Company", event.Company).  // Only if not empty
//	    List("Tags", event.Tags)
//	rendered := dv.Render()
//
// With sections:
//
//	dv := widgets.NewDetailView(theme).
//	    Title("User Profile").
//	    Section("Personal Info").
//	    Field("Name", user.Name).
//	    Field("Email", user.Email).
//	    Section("Work Info").
//	    Field("Company", user.Company).
//	    Field("Role", user.Role)
//
// With lists:
//
//	dv := widgets.NewDetailView(theme).
//	    List("Tags", []string{"go", "tui"}).           // "go, tui"
//	    ListWithSeparator("Skills", skills, " | ").     // "go | python | rust"
//	    BulletList("Steps", steps)                      // Bulleted list
//
// ## API Reference
//
// Constructors:
//
//	NewDetailView(theme Theme) *DetailView
//
// Configuration methods (chainable):
//
//	Title(title string) *DetailView       // Set title at top
//	Width(width int) *DetailView          // Set max width for wrapping
//
// Content methods (chainable):
//
//	Section(title string) *DetailView                        // Start new section
//	Field(label, value string) *DetailView                   // Add field
//	FieldIf(label, value string) *DetailView                 // Add field if value non-empty
//	List(label string, values []string) *DetailView          // Add comma-separated list
//	ListIf(label string, values []string) *DetailView        // Add list if non-empty
//	ListWithSeparator(label string, values []string, sep string) *DetailView
//	BulletList(label string, values []string) *DetailView    // Add bulleted list
//
// Rendering:
//
//	Render() string   // Generate styled output
//
// Theme integration (inherited from theme.Aware):
//
//	SetTheme(theme Theme)   // Update theme
//	Theme() Theme           // Get current theme (default if nil)
//
// ## Text Wrapping
//
// When Width() is set, long text values are automatically wrapped to fit within the specified width. The wrapping algorithm:
//   - Splits text on word boundaries
//   - Handles very long words gracefully (no breaking)
//   - Preserves existing line breaks in multi-line values
//   - Minimum width of 10 characters enforced
//
// ## Best Practices
//
//  1. Always set Width() for responsive layouts
//  2. Use FieldIf/ListIf for optional fields to avoid empty lines
//  3. Group related fields using Section()
//  4. Use BulletList for step-by-step instructions or long lists
//  5. Use List with custom separator for inline display of items
package widgets
