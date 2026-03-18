// Package feedback provides modal and feedback components.
//
// # Overview
//
// The feedback package implements modal dialogs and feedback components
// for user interaction. These components handle confirmations, alerts,
// loading states, and detailed information display.
//
// # Available Components
//
//   - Modal: Base modal with overlay support
//   - ConfirmModal: Yes/No confirmation dialog
//   - DetailModal: Scrollable detail view
//   - InfoModal: Information display with markdown support
//   - HelpModal: Context-sensitive help display
//   - ModalContainer: Modal orchestration and rendering
//
// # Usage
//
// Create a confirmation modal:
//
//	modal := feedback.NewConfirmModal(theme, "Delete item?", func(confirmed bool) {
//	    if confirmed {
//	        // Handle deletion
//	    }
//	})
//
// Render over a screen:
//
//	return behaviors.RenderModalOverlay(modal, screen.View())
package feedback
