package tracer

import (
	"context"
	"log/slog"
	"time"

	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
)

// Hook returns a hook.Hook that wraps each provider call with timing and slog logging.
// It records request message count, response chunk count, duration, and any error.
func Hook() hook.Hook {
	return func(next hook.HandlerFunc) hook.HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			start := time.Now()
			msgCount := len(req.Messages)

			ch, err := next(ctx, req)
			if err != nil {
				slog.Error("tracer provider call failed",
					"messages", msgCount,
					"durationMs", time.Since(start).Milliseconds(),
					"error", err,
				)
				return ch, err
			}

			out := make(chan provider.StreamChunk, cap(ch)+1)
			go func() {
				defer close(out)
				chunkCount := 0
				for chunk := range ch {
					chunkCount++
					out <- chunk
				}
				slog.Info("tracer provider call complete",
					"messages", msgCount,
					"chunks", chunkCount,
					"durationMs", time.Since(start).Milliseconds(),
				)
			}()

			return out, nil
		}
	}
}
