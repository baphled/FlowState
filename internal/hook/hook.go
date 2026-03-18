// Package hook provides middleware hooks for request processing.
package hook

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/skill"
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
func NewChain(hooks ...Hook) *Chain {
	return &Chain{hooks: hooks}
}

// Execute applies all hooks in the chain to the given handler.
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

// LoggingHook returns a hook that logs request timing.
func LoggingHook() Hook {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			log.Printf("[hook] request started with %d messages", len(req.Messages))
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

// ContextInjectionHook returns a hook that injects skill content into the system prompt.
// ContextInjectionHook creates a hook that injects active skill context.
func ContextInjectionHook(skills []skill.Skill, activeSkillNames []string) Hook {
	activeSet := buildActiveSkillSet(activeSkillNames)
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			skillContent := buildSkillContent(skills, activeSet)
			injectSkillContent(req, skillContent)
			return next(ctx, req)
		}
	}
}

func buildActiveSkillSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

func buildSkillContent(skills []skill.Skill, activeSet map[string]bool) string {
	var builder strings.Builder
	for i := range skills {
		if activeSet[skills[i].Name] && skills[i].Content != "" {
			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}
			builder.WriteString(skills[i].Content)
		}
	}
	return builder.String()
}

func injectSkillContent(req *provider.ChatRequest, content string) {
	if content == "" || len(req.Messages) == 0 {
		return
	}
	if req.Messages[0].Role == "system" {
		req.Messages[0].Content = req.Messages[0].Content + "\n\n" + content
	}
}

func extractUserMessage(messages []provider.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}
