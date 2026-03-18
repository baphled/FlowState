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

type HandlerFunc func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error)

type Hook func(next HandlerFunc) HandlerFunc

type Chain struct {
	hooks []Hook
}

func NewChain(hooks ...Hook) *Chain {
	return &Chain{hooks: hooks}
}

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

func ContextInjectionHook(skills []skill.Skill, activeSkillNames []string) Hook {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			activeSet := make(map[string]bool)
			for _, name := range activeSkillNames {
				activeSet[name] = true
			}

			var contentBuilder strings.Builder
			for _, s := range skills {
				if activeSet[s.Name] && s.Content != "" {
					if contentBuilder.Len() > 0 {
						contentBuilder.WriteString("\n\n")
					}
					contentBuilder.WriteString(s.Content)
				}
			}

			if contentBuilder.Len() > 0 && len(req.Messages) > 0 && req.Messages[0].Role == "system" {
				req.Messages[0].Content = req.Messages[0].Content + "\n\n" + contentBuilder.String()
			}

			return next(ctx, req)
		}
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
