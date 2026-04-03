package chat

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
)

func TestSessionViewIntent_Init_ReturnsNil(t *testing.T) {
	intent := NewSessionViewIntent(SessionViewIntentConfig{
		SessionID: "test-session-123",
		Content:   "test content",
		Width:     80,
		Height:    24,
	})

	cmd := intent.Init()

	assert.Nil(t, cmd, "Init should return nil, no initial commands needed")
}

func TestSessionViewIntent_Update_HandlesEsc(t *testing.T) {
	intent := NewSessionViewIntent(SessionViewIntentConfig{
		SessionID: "test-session-123",
		Content:   "test content",
		Width:     80,
		Height:    24,
	})

	cmd := intent.Update(tea.KeyMsg{Type: tea.KeyEsc})

	assert.Nil(t, cmd, "Update with Esc should return nil")
	assert.NotNil(t, intent.Result(), "Result should not be nil after Esc")
}

func TestSessionViewIntent_Update_HandlesScrollKeys(t *testing.T) {
	intent := NewSessionViewIntent(SessionViewIntentConfig{
		SessionID: "test-session-123",
		Content:   "line1\nline2\nline3\nline4\nline5",
		Width:     80,
		Height:    24,
	})

	initialResult := intent.Update(tea.KeyMsg{Type: tea.KeyDown})

	assert.Nil(t, initialResult, "Update with scroll key should return nil")

	_ = intent.Update(tea.KeyMsg{Type: tea.KeyUp})
}

func TestSessionViewIntent_View_RendersContent(t *testing.T) {
	intent := NewSessionViewIntent(SessionViewIntentConfig{
		SessionID: "test-session-123",
		Content:   "test content here",
		Width:     80,
		Height:    24,
	})

	view := intent.View()

	assert.Contains(t, view, "test-ses", "View should contain truncated session ID")
	assert.Contains(t, view, "test content here", "View should contain content")
}

func TestSessionViewIntent_View_ShowsBreadcrumb(t *testing.T) {
	intent := NewSessionViewIntent(SessionViewIntentConfig{
		SessionID: "abc12345-session",
		Content:   "test",
		Width:     80,
		Height:    24,
	})

	view := intent.View()

	assert.Contains(t, view, "Chat > abc12345", "View should show breadcrumb path")
}

func TestSessionViewIntent_Result_ReturnsNilInitially(t *testing.T) {
	intent := NewSessionViewIntent(SessionViewIntentConfig{
		SessionID: "test-session-123",
		Content:   "test content",
		Width:     80,
		Height:    24,
	})

	result := intent.Result()

	assert.Nil(t, result, "Result should be nil initially")
}

func TestSessionViewIntent_Result_AfterEsc(t *testing.T) {
	intent := NewSessionViewIntent(SessionViewIntentConfig{
		SessionID: "test-session-123",
		Content:   "test content",
		Width:     80,
		Height:    24,
	})

	intent.Update(tea.KeyMsg{Type: tea.KeyEsc})
	result := intent.Result()

	assert.NotNil(t, result, "Result should not be nil after Esc")
	assert.Equal(t, "navigate_parent", result.Action, "Action should be navigate_parent")
}

func TestSessionViewIntent_ImplementsIntentInterface(t *testing.T) {
	var _ tuiintents.Intent = (*SessionViewIntent)(nil)
}
