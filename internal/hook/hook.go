// Package hook provides middleware hooks for request processing.
package hook

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/provider"
)

const streamBufferSize = 16

// HandlerFunc is the signature for chat request handlers.
type HandlerFunc func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error)

// Hook wraps a handler to add middleware functionality.
type Hook func(next HandlerFunc) HandlerFunc

// Chain manages a sequence of hooks to execute.
type Chain struct {
	hooks []Hook
}

// NewChain creates a new hook chain from the given hooks.
//
// Expected:
//   - hooks is a variadic list of Hook middleware functions.
//
// Returns:
//   - A configured Chain containing the provided hooks.
//
// Side effects:
//   - None.
func NewChain(hooks ...Hook) *Chain {
	return &Chain{hooks: hooks}
}

// Len returns the number of hooks in the chain.
//
// Returns:
//   - The count of hooks in the chain.
//
// Side effects:
//   - None.
func (c *Chain) Len() int {
	return len(c.hooks)
}

// Execute applies all hooks in the chain to the given handler.
//
// Expected:
//   - handler is the base HandlerFunc to wrap with middleware.
//
// Returns:
//   - A HandlerFunc with all hooks applied in order.
//
// Side effects:
//   - None.
func (c *Chain) Execute(handler HandlerFunc) HandlerFunc {
	if len(c.hooks) == 0 {
		return handler
	}

	wrapped := handler
	for i := len(c.hooks) - 1; i >= 0; i-- {
		wrapped = c.hooks[i](wrapped)
	}
	return wrapped
}

// LoggingHook returns a hook that logs request timing and message counts.
//
// Returns:
//   - A Hook that wraps handlers with timing and message count logging.
//
// Side effects:
//   - Logs request start, completion, and failures to the standard logger.
func LoggingHook() Hook {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			// Surface the tool count so "no tools attached" bugs show up on
			// the first log line instead of hiding until someone reads the
			// raw session JSON. Diagnostic for session-1776611908809856897,
			// where the planner emitted tool-call-shaped JSON as content
			// and operators could not tell from logs whether req.Tools was
			// empty at the moment of the stream request.
			log.Printf("[hook] request started with %d messages tools=%d", len(req.Messages), len(req.Tools))
			start := time.Now()

			resultChan, err := next(ctx, req)
			if err != nil {
				log.Printf("[hook] request failed: %v", err)
				return nil, err
			}

			outChan := make(chan provider.StreamChunk, streamBufferSize)
			go func() {
				defer close(outChan)
				for chunk := range resultChan {
					outChan <- chunk
				}
				log.Printf("[hook] request completed in %v", time.Since(start))
			}()

			return outChan, nil
		}
	}
}

// LearningHook creates a hook that records learning entries from conversations.
//
// Expected:
//   - store is a non-nil learning.Store for persisting entries.
//
// Returns:
//   - A Hook that captures conversation exchanges as learning entries.
//
// Side effects:
//   - Writes learning entries to the provided store after each response completes.
func LearningHook(store learning.Store) Hook {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			resultChan, err := next(ctx, req)
			if err != nil {
				return nil, err
			}

			userMessage := extractUserMessage(req.Messages)

			outChan := make(chan provider.StreamChunk, streamBufferSize)
			go func() {
				defer close(outChan)
				var responseBuilder strings.Builder

				for chunk := range resultChan {
					responseBuilder.WriteString(chunk.Content)
					outChan <- chunk
				}

				entry := learning.Entry{
					Timestamp:   time.Now(),
					UserMessage: userMessage,
					Response:    responseBuilder.String(),
				}
				if err := store.Capture(entry); err != nil {
					log.Printf("warning: %v", err)
				}
			}()

			return outChan, nil
		}
	}
}

// extractUserMessage retrieves the most recent user message from a message slice.
//
// Expected:
//   - messages is a slice of provider messages (may be empty).
//
// Returns:
//   - The content of the last user message found.
//   - An empty string if no user message exists.
//
// Side effects:
//   - None.
func extractUserMessage(messages []provider.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

// LearningLoopHook creates a hook that notifies a TriggerSink after each response.
//
// When the response completes, the hook raises a TriggerKindFailure trigger if
// the last chunk carries an error, or a TriggerKindNovelty trigger otherwise.
// The trigger is fire-and-forget: it never blocks the streaming path.
//
// Expected:
//   - agentID identifies the agent whose responses are being observed.
//   - sink is a non-nil learning.TriggerSink.
//
// Returns:
//   - A Hook that notifies the sink after each completed response.
//
// Side effects:
//   - Calls TriggerSink.Notify in the goroutine that drains the response channel.
func LearningLoopHook(agentID string, sink learning.TriggerSink) Hook {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			resultChan, err := next(ctx, req)
			if err != nil {
				return nil, err
			}

			outChan := make(chan provider.StreamChunk, streamBufferSize)
			go func() {
				defer close(outChan)
				var responseBuilder strings.Builder
				var lastErr bool

				for chunk := range resultChan {
					if chunk.Error != nil {
						lastErr = true
					}
					responseBuilder.WriteString(chunk.Content)
					outChan <- chunk
				}

				kind := learning.TriggerKindNovelty
				if lastErr {
					kind = learning.TriggerKindFailure
				}
				sink.Notify(learning.Trigger{
					ID:       time.Now().Format(time.RFC3339Nano),
					AgentID:  agentID,
					Kind:     kind,
					Source:   learning.TriggerSourceLearningHook,
					Output:   responseBuilder.String(),
					RaisedAt: time.Now(),
				})
			}()

			return outChan, nil
		}
	}
}
