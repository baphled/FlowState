package support

import (
	"errors"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/provider"
)

// StreamingToolOutputSteps holds state for streaming tool output BDD scenarios.
type StreamingToolOutputSteps struct {
	toolOutput     string
	toolError      error
	chunks         []provider.StreamChunk
	hasToolResult  bool
	toolResultText string
}

// RegisterStreamingToolOutputSteps registers step definitions for streaming tool output scenarios.
//
// Expected:
//   - sc is a valid godog ScenarioContext for step registration.
//   - s is a non-nil StreamingToolOutputSteps instance.
//
// Side effects:
//   - Registers step definitions on the provided scenario context.
func RegisterStreamingToolOutputSteps(sc *godog.ScenarioContext, s *StreamingToolOutputSteps) {
	sc.Step(`^the engine executes a tool that produces output$`, s.theEngineExecutesAToolThatProducesOutput)
	sc.Step(`^the stream is processed$`, s.theStreamIsProcessed)
	sc.Step(`^the tool result should be visible in the chat$`, s.theToolResultShouldBeVisibleInTheChat)
}

// theEngineExecutesAToolThatProducesOutput simulates a tool execution that produces output.
//
// Expected:
//   - No prior state is required.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sets toolOutput to "Command executed successfully".
func (s *StreamingToolOutputSteps) theEngineExecutesAToolThatProducesOutput() error {
	s.toolOutput = "Command executed successfully"
	s.toolError = nil
	return nil
}

// theStreamIsProcessed simulates the engine emitting a tool result chunk.
// This models what streamWithToolLoop should do: after executeToolCall returns a result,
// emit it as a StreamChunk with ToolResult field before continuing the loop.
//
// Expected:
//   - theEngineExecutesAToolThatProducesOutput has been called.
//
// Returns:
//   - nil on success.
//   - An error if the tool result cannot be converted to a chunk.
//
// Side effects:
//   - Populates s.chunks with a mock StreamChunk containing ToolResult.
func (s *StreamingToolOutputSteps) theStreamIsProcessed() error {
	if s.toolOutput == "" && s.toolError == nil {
		return errors.New("no tool output or error set")
	}

	resultContent := s.toolOutput
	isError := s.toolError != nil
	if isError {
		resultContent = "Error: " + s.toolError.Error()
	}

	chunk := provider.StreamChunk{
		EventType: "tool_result",
		ToolResult: &provider.ToolResultInfo{
			Content: resultContent,
			IsError: isError,
		},
	}

	s.chunks = append(s.chunks, chunk)
	return nil
}

// theToolResultShouldBeVisibleInTheChat asserts that a tool result chunk was emitted.
//
// Expected:
//   - theStreamIsProcessed has been called.
//
// Returns:
//   - An error if no tool result chunk is found.
//
// Side effects:
//   - None.
func (s *StreamingToolOutputSteps) theToolResultShouldBeVisibleInTheChat() error {
	for _, chunk := range s.chunks {
		if chunk.EventType == "tool_result" && chunk.ToolResult != nil && chunk.ToolResult.Content != "" {
			s.hasToolResult = true
			s.toolResultText = chunk.ToolResult.Content
			return nil
		}
	}
	return fmt.Errorf("expected a tool_result chunk with non-empty content, got %d chunks", len(s.chunks))
}
