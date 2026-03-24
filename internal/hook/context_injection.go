package hook

import (
	"context"

	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
)

const (
	contextInjectionMarker = "## Codebase Context"
	maxContextSize         = 2 * 1024 // 2KB
	cacheRefreshInterval   = 5 * time.Minute
)

type contextCache struct {
	mu        sync.Mutex
	content   string
	lastBuilt time.Time
}

func (c *contextCache) get(projectRoot string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.lastBuilt) < cacheRefreshInterval && c.content != "" {
		return c.content
	}
	c.content = buildCacheContext(projectRoot)
	c.lastBuilt = time.Now()
	return c.content
}

func buildCacheContext(projectRoot string) string {
	var sb strings.Builder
	sb.WriteString("## Codebase Context\n")

	if out, err := exec.Command("git", "-C", projectRoot, "log", "--oneline", "-5").Output(); err == nil { //nolint:gosec // trusted path
		sb.WriteString("### Recent Changes\n")
		sb.WriteString(string(out))
		sb.WriteString("\n")
	}

	sb.WriteString("### Key Go Packages\n")
	sb.WriteString("internal/plan, internal/hook, internal/engine, internal/provider, internal/app\n\n")

	result := sb.String()
	if len(result) > maxContextSize {
		result = result[:maxContextSize]
	}
	return result
}

// ContextInjectionHook returns a hook that injects codebase context into the planner's system prompt.
//
// Expected:
//   - manifestGetter returns the current agent manifest on each call.
//   - projectRoot is the absolute path to the project root directory.
//
// Returns:
//   - A Hook that prepends codebase context on the first planner message only.
//
// Side effects:
//   - Reads git log on first call; cached for 5 minutes.
func ContextInjectionHook(manifestGetter func() agent.Manifest, projectRoot string) Hook {
	cache := &contextCache{}
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			manifest := manifestGetter()
			if !strings.Contains(strings.ToLower(manifest.ID), "planner") {
				return next(ctx, req)
			}
			if containsAssistantMessage(req.Messages) {
				return next(ctx, req)
			}
			if len(req.Messages) > 0 && req.Messages[0].Role == "system" &&
				strings.Contains(req.Messages[0].Content, contextInjectionMarker) {
				return next(ctx, req)
			}
			codebaseCtx := cache.get(projectRoot)
			injectLeanSkills(req, codebaseCtx)
			return next(ctx, req)
		}
	}
}
