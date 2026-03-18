package widgets

import tea "github.com/charmbracelet/bubbletea"

// View handles only the content section inside an intent.
// The intent owns chrome (breadcrumbs, logo, footer) via ScreenLayout.
type View interface {
	// RenderContent returns the content string — no chrome, no layout wrapper.
	// The intent passes this to ScreenLayout.WithContent().
	RenderContent() string

	// HelpText returns contextual key binding hints.
	// The intent passes this to ScreenLayout.WithHelp().
	HelpText() string

	// Init returns initial tea.Cmd for async loading or subscriptions.
	Init() tea.Cmd

	// Update handles a tea.Msg and returns a command and optional ViewResult.
	// Returns nil ViewResult when only internal state changes.
	// Returns non-nil ViewResult when the view's purpose is complete.
	Update(msg tea.Msg) (tea.Cmd, ViewResult)
}

// ViewResultType indicates the type of result a View is returning.
type ViewResultType string

const (
	// ResultNavigate indicates the user selected something or wants to move forward.
	ResultNavigate ViewResultType = "navigate"

	// ResultCancel indicates the user pressed Escape or wants to go back.
	ResultCancel ViewResultType = "cancel"

	// ResultSubmit indicates the user submitted a form or completed an action.
	ResultSubmit ViewResultType = "submit"

	// ResultError indicates an error occurred in the View.
	ResultError ViewResultType = "error"
)

// ViewResult represents the outcome of a View's Update operation.
type ViewResult interface {
	// Type returns the type of result (Navigate, Cancel, Submit, Error).
	//
	// Returns:
	//   - A ViewResultType value.
	//
	// Side effects:
	//   - None.
	Type() ViewResultType

	// Data returns the result payload.
	Data() interface{}

	// Metadata returns additional context for the result.
	Metadata() map[string]interface{}

	// WithMetadata adds metadata to the result (fluent API).
	WithMetadata(key string, value interface{}) ViewResult
}

// NoOpViewResult returns nil, used by static views that never produce results.
//
// Returns:
//   - A ViewResult value.
//
// Side effects:
//   - None.
func NoOpViewResult() ViewResult {
	return nil
}

// Module provides context-specific rendering overrides for a View.
// Use when a View needs different content or help text depending on
// whether it is rendered in a screen or modal context.
type Module interface {
	// RenderContent overrides the view's content for this context.
	RenderContent(v View) string

	// HelpText overrides the view's help text for this context.
	HelpText(v View) string
}

// NavigateViewResult is returned when the user selects an item or wants to move forward.
type NavigateViewResult struct {
	ResultData interface{}
	Meta       map[string]interface{}
}

// Type returns ResultNavigate.
//
// Returns:
//   - A ViewResultType value.
//
// Side effects:
//   - None.
func (r *NavigateViewResult) Type() ViewResultType { return ResultNavigate }

// Data returns the navigation payload.
//
// Returns:
//   - A interface{} value.
//
// Side effects:
//   - None.
func (r *NavigateViewResult) Data() interface{} { return r.ResultData }

// Metadata returns the metadata map.
//
// Returns:
//   - A map[string]interface{} value.
//
// Side effects:
//   - None.
func (r *NavigateViewResult) Metadata() map[string]interface{} {
	if r.Meta == nil {
		r.Meta = make(map[string]interface{})
	}
	return r.Meta
}

// WithMetadata adds a key-value pair to the metadata.
//
// Expected:
//   - Must be a valid string.
//   - interface{} must be valid.
//
// Returns:
//   - A ViewResult value.
//
// Side effects:
//   - None.
func (r *NavigateViewResult) WithMetadata(key string, value interface{}) ViewResult {
	if r.Meta == nil {
		r.Meta = make(map[string]interface{})
	}
	r.Meta[key] = value
	return r
}

// CancelViewResult is returned when the user cancels or presses Escape.
type CancelViewResult struct {
	Meta map[string]interface{}
}

// Type returns ResultCancel.
//
// Returns:
//   - A ViewResultType value.
//
// Side effects:
//   - None.
func (r *CancelViewResult) Type() ViewResultType { return ResultCancel }

// Data always returns nil.
//
// Returns:
//   - A interface{} value.
//
// Side effects:
//   - None.
func (r *CancelViewResult) Data() interface{} { return nil }

// Metadata returns the metadata map.
//
// Returns:
//   - A map[string]interface{} value.
//
// Side effects:
//   - None.
func (r *CancelViewResult) Metadata() map[string]interface{} {
	if r.Meta == nil {
		r.Meta = make(map[string]interface{})
	}
	return r.Meta
}

// WithMetadata adds a key-value pair to the metadata.
//
// Expected:
//   - Must be a valid string.
//   - interface{} must be valid.
//
// Returns:
//   - A ViewResult value.
//
// Side effects:
//   - None.
func (r *CancelViewResult) WithMetadata(key string, value interface{}) ViewResult {
	if r.Meta == nil {
		r.Meta = make(map[string]interface{})
	}
	r.Meta[key] = value
	return r
}

// SubmitViewResult is returned when the user submits a form or completes an action.
type SubmitViewResult struct {
	FormData interface{}
	Meta     map[string]interface{}
}

// Type returns ResultSubmit.
//
// Returns:
//   - A ViewResultType value.
//
// Side effects:
//   - None.
func (r *SubmitViewResult) Type() ViewResultType { return ResultSubmit }

// Data returns the submitted form data.
//
// Returns:
//   - A interface{} value.
//
// Side effects:
//   - None.
func (r *SubmitViewResult) Data() interface{} { return r.FormData }

// Metadata returns the metadata map.
//
// Returns:
//   - A map[string]interface{} value.
//
// Side effects:
//   - None.
func (r *SubmitViewResult) Metadata() map[string]interface{} {
	if r.Meta == nil {
		r.Meta = make(map[string]interface{})
	}
	return r.Meta
}

// WithMetadata adds a key-value pair to the metadata.
//
// Expected:
//   - Must be a valid string.
//   - interface{} must be valid.
//
// Returns:
//   - A ViewResult value.
//
// Side effects:
//   - None.
func (r *SubmitViewResult) WithMetadata(key string, value interface{}) ViewResult {
	if r.Meta == nil {
		r.Meta = make(map[string]interface{})
	}
	r.Meta[key] = value
	return r
}

// ErrorViewResult is returned when an error occurs in the View.
type ErrorViewResult struct {
	Err     error
	Message string
	Meta    map[string]interface{}
}

// Type returns ResultError.
//
// Returns:
//   - A ViewResultType value.
//
// Side effects:
//   - None.
func (r *ErrorViewResult) Type() ViewResultType { return ResultError }

// Data returns a map with "error" and "message" keys.
//
// Returns:
//   - A interface{} value.
//
// Side effects:
//   - None.
func (r *ErrorViewResult) Data() interface{} {
	return map[string]interface{}{
		"error":   r.Err,
		"message": r.Message,
	}
}

// Metadata returns the metadata map.
//
// Returns:
//   - A map[string]interface{} value.
//
// Side effects:
//   - None.
func (r *ErrorViewResult) Metadata() map[string]interface{} {
	if r.Meta == nil {
		r.Meta = make(map[string]interface{})
	}
	return r.Meta
}

// WithMetadata adds a key-value pair to the metadata.
//
// Expected:
//   - Must be a valid string.
//   - interface{} must be valid.
//
// Returns:
//   - A ViewResult value.
//
// Side effects:
//   - None.
func (r *ErrorViewResult) WithMetadata(key string, value interface{}) ViewResult {
	if r.Meta == nil {
		r.Meta = make(map[string]interface{})
	}
	r.Meta[key] = value
	return r
}
