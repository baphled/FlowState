// Package hook defines the middleware surface wrapping provider
// dispatch handlers.
package hook

import (
	"context"

	"github.com/baphled/flowstate/internal/provider"
)

// HandlerFunc performs one provider stream attempt.
type HandlerFunc func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error)
