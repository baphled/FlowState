package provider

import (
	"testing"
)

func TestToolResultInfo(t *testing.T) {
	info := &ToolResultInfo{
		Content: "output",
		IsError: false,
	}

	if info.Content != "output" {
		t.Errorf("expected Content to be 'output', got %q", info.Content)
	}
	if info.IsError != false {
		t.Errorf("expected IsError to be false, got %v", info.IsError)
	}
}

func TestStreamChunkWithToolResult(t *testing.T) {
	chunk := StreamChunk{
		Content:   "test",
		Done:      false,
		EventType: "tool_result",
		ToolResult: &ToolResultInfo{
			Content: "tool output",
			IsError: false,
		},
	}

	if chunk.ToolResult == nil {
		t.Fatal("expected ToolResult to be non-nil")
	}
	if chunk.ToolResult.Content != "tool output" {
		t.Errorf("expected ToolResult.Content to be 'tool output', got %q", chunk.ToolResult.Content)
	}
}
